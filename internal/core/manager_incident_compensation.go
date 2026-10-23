package core

import (
	"context"
	"errors"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Time given back for an outage: once an incident or a maintenance window is over,
// the operator adds hours to the subscriptions of the users it hit — those whose term
// was running when it began and who may use one of the servers it names. It is done
// once per incident.

// CompensationHoursMax bounds what one incident gives back: a month and more of
// outage is a refund, not a make-up.
const CompensationHoursMax = 24 * 90

// CompensationHours is what an incident's length suggests giving back: its whole
// hours, rounded up, at least one. 0 while it is not over.
func CompensationHours(in *model.Incident, now int64) int {
	end := in.EndedAt(now)
	if end == 0 {
		return 0
	}
	secs := max(end-in.StartsAt, 1)
	return int(min((secs+3599)/3600, CompensationHoursMax))
}

// CompensateIncident adds hours to the subscriptions of the users the incident hit
// and, when notify, tells each of them through the user bot. It returns the incident
// as stored, compensation and all.
func (m *Manager) CompensateIncident(ctx context.Context, id int64, hours int, notify bool) (*model.Incident, error) {
	if hours < 1 || hours > CompensationHoursMax {
		return nil, invalidCode("err.compensationHours", "укажите от 1 до {{max}} часов", map[string]any{"max": CompensationHoursMax})
	}
	in, err := m.store.GetIncident(id)
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, invalidCode("err.incidentNotFound", "запись не найдена")
	}
	now := time.Now().Unix()
	end := in.EndedAt(now)
	if end == 0 {
		return nil, invalidCode("err.compensationNotOver", "компенсировать можно только завершённую запись")
	}
	if in.CompensatedAt != 0 {
		return nil, invalidCode("err.compensationDone", "за эту запись уже компенсировали")
	}
	add := int64(hours) * 3600

	// expire_at is read-modify-written, under the lock every plan and payment write
	// holds, as the bulk extension does — the read included, or a payment landing in
	// between would leave its user out.
	m.applyPlanMu.Lock()
	cands, err := m.compensationTargets(in, end)
	if err != nil {
		m.applyPlanMu.Unlock()
		return nil, err
	}
	from := make(map[int64]int64, len(cands))
	to := make(map[int64]int64, len(cands))
	for _, c := range cands {
		// A term that ran out since gets the hours from now: they are what it lost.
		from[c.ID] = c.ExpireAt
		to[c.ID] = max(c.ExpireAt, now) + add
	}
	moved, err := m.store.CompensateIncident(id, now, add, from, to)
	m.applyPlanMu.Unlock()
	if errors.Is(err, store.ErrCompensated) {
		return nil, invalidCode("err.compensationDone", "за эту запись уже компенсировали")
	}
	if err != nil {
		return nil, err
	}

	byID := make(map[int64]store.CompensationCandidate, len(cands))
	for _, c := range cands {
		byID[c.ID] = c
	}
	for _, uid := range moved {
		c := byID[uid]
		m.auditNamed(ctx, uid, c.Name, model.EventUserLimits, map[string]any{
			"expire_at": to[uid], "compensated_hours": hours,
			"incident_id": id, "incident": in.Title,
		})
	}
	m.emitUsersWebhook(model.WebhookUserLimitsChanged, moved, map[string]any{
		"compensated_hours": hours, "incident_id": id,
	})
	if len(moved) > 0 {
		m.TriggerUserSync()
	}
	logInfo("incident compensated", "incident", id, "hours", hours, "users", len(moved))

	saved, err := m.store.GetIncident(id)
	if err != nil {
		return nil, err
	}
	if saved == nil {
		return nil, invalidCode("err.incidentNotFound", "запись не найдена")
	}
	m.incidentsChanged()
	m.emitIncident(model.WebhookIncidentUpdated, saved, nil)

	if notify {
		m.tellCompensated(saved, hours, moved, byID, to)
	}
	return saved, nil
}

// tellCompensated messages each compensated user who has the bot, in the background:
// one by one through the bot's own pace, since a compensation can reach more users
// than the notification queue holds.
func (m *Manager) tellCompensated(in *model.Incident, hours int, moved []int64, byID map[int64]store.CompensationCandidate, to map[int64]int64) {
	set, err := m.store.GetSettings()
	send := m.messenger()
	if err != nil || !set.TGUserBotEnabled || send == nil {
		return
	}
	type msg struct {
		chat int64
		text string
	}
	var out []msg
	for _, uid := range moved {
		if c := byID[uid]; c.TgChatID != 0 {
			out = append(out, msg{c.TgChatID, m.compensationMessage(in, hours, to[uid], m.userLang(c.TgChatID))})
		}
	}
	if len(out) == 0 {
		return
	}
	go func() {
		failed := 0
		for _, x := range out {
			if err := send(x.chat, x.text, nil); err != nil {
				failed++
			}
		}
		if failed > 0 {
			logErr("incident compensation: messages not delivered", "incident", in.ID, "failed", failed, "of", len(out))
		}
	}()
}

// compensationTargets are the users an incident that ended at end hit: their term was
// running when it began, and they may use a server it names (any, when it names none).
func (m *Manager) compensationTargets(in *model.Incident, end int64) ([]store.CompensationCandidate, error) {
	cands, err := m.store.CompensationCandidates(in, end)
	if err != nil {
		return nil, err
	}
	if len(in.NodeIDs) == 0 {
		return cands, nil
	}
	access, err := m.store.AccessMap()
	if err != nil {
		return nil, err
	}
	inbounds, err := m.store.AllInbounds()
	if err != nil {
		return nil, err
	}
	inboundServer := map[int64]int64{}
	for sid, list := range inbounds {
		for _, ib := range list {
			inboundServer[ib.ID] = sid
		}
	}
	reaches := func(a model.Access) bool {
		if a.All {
			return true
		}
		for token := range a.Tokens {
			if sid, _, ok := model.ParseBuiltinToken(token); ok && in.Concerns(sid) {
				return true
			}
			if iid, ok := model.ParseInboundToken(token); ok {
				if sid, known := inboundServer[iid]; known && in.Concerns(sid) {
					return true
				}
			}
		}
		return false
	}
	out := cands[:0]
	for _, c := range cands {
		if reaches(model.AccessOf(access, c.ID)) {
			out = append(out, c)
		}
	}
	return out, nil
}

// compensationMessage is the bot's word to one user: what happened, what they got,
// and until when their subscription now runs.
func (m *Manager) compensationMessage(in *model.Incident, hours int, expire int64, lang i18n.Lang) string {
	amount := i18n.TN(lang, "notify.hours", hours)
	if hours%24 == 0 {
		amount = i18n.TN(lang, "notify.days", hours/24)
	}
	until := time.Unix(expire, 0).In(m.loc()).Format("02.01.2006 15:04")
	return i18n.T(lang, "notify.compensated", escHTML(in.Title), amount, until)
}
