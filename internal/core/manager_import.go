package core

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/importer"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/google/uuid"
)

// Importing users from another panel is two steps on purpose: a preview that
// reads the file and says what would happen, then a write of exactly the rows the
// operator ticked. The file is never kept; what comes back to the second step is
// the candidates themselves.

// MaxImportBatch caps one import call. Well above any real migration, and low
// enough that a runaway client cannot hold the writer for minutes.
const MaxImportBatch = 5000

// ImportCandidate is an importer.Candidate annotated with what this panel
// already has for it.
type ImportCandidate struct {
	importer.Candidate
	// Exists means a user with this UUID is already here — the same person,
	// imported earlier (or created by hand with the same id). The import skips
	// them, so running the same file twice cannot double anyone.
	Exists     bool  `json:"exists"`
	ExistingID int64 `json:"existing_id,omitempty"`
	// NameTaken says a user with this name exists; names are not unique here, so
	// this is information for the operator, not a block.
	NameTaken bool `json:"name_taken"`
}

// ImportPreview is what the inspect step answers with.
type ImportPreview struct {
	Source importer.Source   `json:"source"`
	Users  []ImportCandidate `json:"users"`
}

// ImportPreview reads the file at path and annotates its users against this
// panel. Nothing is written.
func (m *Manager) ImportPreview(path string) (*ImportPreview, error) {
	source, users, err := importer.Parse(path)
	if err != nil {
		if errors.Is(err, importer.ErrUnknownFormat) {
			return nil, invalidCode("err.importUnknownFormat", "файл не похож на базу Marzban / 3x-ui или их JSON-выгрузку")
		}
		return nil, err
	}
	existing, err := m.store.UserUUIDs()
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	if all, err := m.store.ListUsers(); err == nil {
		for _, u := range all {
			names[strings.ToLower(u.Name)] = true
		}
	}
	out := &ImportPreview{Source: source, Users: make([]ImportCandidate, 0, len(users))}
	for _, c := range users {
		ic := ImportCandidate{Candidate: c}
		if id, ok := existing[strings.ToLower(c.UUID)]; ok {
			ic.Exists, ic.ExistingID = true, id
		}
		ic.NameTaken = names[strings.ToLower(c.Name)]
		out.Users = append(out.Users, ic)
	}
	return out, nil
}

// ImportRequest is the second step: the candidates the operator kept, and the
// tags to put on every one of them (so the batch can be found again).
type ImportRequest struct {
	Source string               `json:"source"`
	Users  []importer.Candidate `json:"users"`
	Tags   []string             `json:"tags"`
}

// ImportFailure is one candidate that could not be created, with the error code
// the panel renders.
type ImportFailure struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

// ImportResult says what the import did. Skipped counts UUIDs that were already
// here; Failed lists what was refused (a bad limit, an insert error) by name.
type ImportResult struct {
	Created int             `json:"created"`
	Skipped int             `json:"skipped"`
	Failed  []ImportFailure `json:"failed"`
}

// ImportUsers creates the given candidates. Each one is validated the way a
// hand-made user is; one that fails is reported and the rest go in. Xray is
// synced once at the end, like a bulk action, not once per user.
func (m *Manager) ImportUsers(ctx context.Context, req ImportRequest) (*ImportResult, error) {
	if len(req.Users) == 0 {
		return nil, invalidCode("err.importNothingSelected", "не выбрано ни одного пользователя")
	}
	if len(req.Users) > MaxImportBatch {
		return nil, invalidCode("err.importTooMany", "за один раз можно импортировать до {{max}} пользователей", map[string]any{"max": MaxImportBatch})
	}
	tags, ok := model.NormalizeTags(req.Tags)
	if !ok {
		return nil, invalidCode("err.tagsInvalid", "тег: до {{maxLen}} символов, без запятых, не больше {{max}} тегов",
			map[string]any{"maxLen": model.MaxUserTagLen, "max": model.MaxUserTags})
	}
	existing, err := m.store.UserUUIDs()
	if err != nil {
		return nil, err
	}
	// Subscription tokens are unique, so a carried one is used only when it is free;
	// otherwise the user gets a fresh link rather than the import failing.
	takenTokens, err := m.store.SubTokens()
	if err != nil {
		return nil, err
	}
	source := strings.TrimSpace(req.Source)
	res := &ImportResult{Failed: []ImportFailure{}}
	fail := func(name, code string) {
		res.Failed = append(res.Failed, ImportFailure{Name: name, Code: code})
	}
	for _, c := range req.Users {
		name, err := cleanUserName(c.Name)
		if err != nil {
			fail(c.Name, codeOf(err))
			continue
		}
		id, err := uuid.Parse(strings.TrimSpace(c.UUID))
		if err != nil {
			fail(name, "err.importBadUUID")
			continue
		}
		if _, dup := existing[strings.ToLower(id.String())]; dup {
			res.Skipped++
			continue
		}
		if err := validateUserLimits(c.DataLimit, c.ExpireAt, c.DeviceLimit); err != nil {
			fail(name, codeOf(err))
			continue
		}
		password := strings.TrimSpace(c.Password)
		if password == "" {
			if password, err = auth.RandomPassword(); err != nil {
				return nil, err
			}
		}
		subToken := strings.TrimSpace(c.SubToken)
		if _, taken := takenTokens[subToken]; subToken == "" || taken {
			if subToken, err = auth.RandomToken(); err != nil {
				return nil, err
			}
		}
		takenTokens[subToken] = struct{}{}
		// The batch tag is added to whatever the user already carried, so an export
		// re-imported elsewhere keeps its labels and gains the one naming the move.
		userTags, ok := model.NormalizeTags(append(append([]string{}, c.Tags...), tags...))
		if !ok {
			fail(name, "err.tagsInvalid")
			continue
		}
		u, err := m.store.ImportUser(store.ImportedUser{
			Name: name, UUID: id.String(), Password: password, SubToken: subToken,
			DataLimit: c.DataLimit, ExpireAt: c.ExpireAt,
			UsedUp: max(c.UsedUp, 0), UsedDown: max(c.UsedDown, 0),
			DeviceLimit: c.DeviceLimit, SpeedLimit: max(c.SpeedLimit, 0),
			ResetPeriod: resetPeriodOr(c.ResetPeriod), Enabled: c.Enabled,
			Note: strings.TrimSpace(c.Note), Tags: userTags,
			WGPrivateKey: strings.TrimSpace(c.WGPrivate),
		})
		if err != nil {
			logErr("import: user insert failed", "name", name, "err", err)
			fail(name, "err.importInsertFailed")
			continue
		}
		// Two imports in one request with the same UUID: the first wins, the
		// second is a skip, not an insert error.
		existing[strings.ToLower(id.String())] = u.ID
		res.Created++
		m.auditNamed(ctx, u.ID, u.Name, model.EventUserCreated, map[string]any{
			"data_limit": u.DataLimit, "expire_at": u.ExpireAt, "imported_from": source,
		})
		m.EmitWebhook(model.WebhookUserCreated, userEventData(*u))
	}
	if res.Created > 0 {
		logInfo("import: users created", "source", source, "created", res.Created, "skipped", res.Skipped, "failed", len(res.Failed))
		m.TriggerUserSync()
	}
	return res, nil
}

// codeOf extracts the dictionary code from a validation error, or falls back to
// a generic one so a failure row always has something the panel can render.
func codeOf(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) && ve.Code != "" {
		return ve.Code
	}
	return "err.importInsertFailed"
}

// resetPeriodOr keeps a carried auto-reset period only when this panel knows it —
// the five named cycles and the rolling "days:N" a plan writes (see resetDue).
// Anything else (a hand-edited file, a newer panel) falls back to no reset rather
// than storing a value the quota sweep would never act on.
func resetPeriodOr(period string) string {
	period = strings.TrimSpace(period)
	switch period {
	case "none", "daily", "weekly", "monthly", "yearly":
		return period
	}
	if spec, ok := strings.CutPrefix(period, "days:"); ok {
		if n, err := strconv.Atoi(spec); err == nil && n > 0 {
			return period
		}
	}
	return "none"
}

// ExportUsers writes every user as this panel's own export file (see
// importer.Export): the credentials that make a move invisible to them, their
// limits, their usage and the operator's annotations.
func (m *Manager) ExportUsers() (*importer.Export, error) {
	users, err := m.store.ListUsers()
	if err != nil {
		return nil, err
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	out := &importer.Export{
		Format:     importer.Format,
		Version:    importer.FormatVersion,
		ExportedAt: time.Now().Unix(),
		Panel:      set.Host,
		Users:      make([]importer.Candidate, 0, len(users)),
	}
	for _, u := range users {
		out.Users = append(out.Users, importer.Candidate{
			Name: u.Name, UUID: u.UUID, Password: u.Password,
			DataLimit: u.DataLimit, ExpireAt: u.ExpireAt,
			UsedUp: u.UsedUp, UsedDown: u.UsedDown,
			DeviceLimit: u.DeviceLimit, Enabled: u.Enabled,
			Note: u.Note, Issues: []string{},
			SubToken: u.SubToken, WGPrivate: u.WGPrivateKey,
			SpeedLimit: u.SpeedLimit, ResetPeriod: u.ResetPeriod, Tags: u.Tags,
		})
	}
	return out, nil
}
