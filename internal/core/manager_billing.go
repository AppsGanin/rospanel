package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/google/uuid"
)

// maxPlanPeriodDays bounds a plan's duration. Generous (100 years) — it exists to keep
// int64(PeriodDays)*86400 far from overflowing into a negative expiry, not to second-guess
// the operator's pricing.
const maxPlanPeriodDays = 36500

// ListTariffPlans returns tariff plans for admin UI.
func (m *Manager) ListTariffPlans(includeDisabled bool) ([]model.TariffPlan, error) {
	return m.store.ListTariffPlans(includeDisabled)
}

func (m *Manager) SaveTariffPlan(p *model.TariffPlan) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return invalidCode("err.planNameRequired", "укажите название тарифа")
	}
	p.Slug = strings.TrimSpace(p.Slug)
	if p.Slug == "" {
		p.Slug = slugifyPlan(p.Name)
	}
	if !slugRe.MatchString(p.Slug) {
		return invalidCode("err.planSlugCharset", "код тарифа: только латинские буквы, цифры и дефис")
	}
	// Price defines the tier: 0 ⇒ free (never expires, quota refills every plan
	// duration via PeriodDays); > 0 ⇒ paid (expires after PeriodDays). There is no
	// separate "free" flag — see model.TariffPlan.IsFree.
	//
	// Which plans may be free is not the operator's free choice: only the two the
	// billing settings designate (free plan, trial plan) are, and they are ALWAYS
	// free. Otherwise a paid plan picked as the free one would be handed out for
	// nothing and forever, and a zero-price plan that is designated as neither is
	// unreachable — filtered out of the purchase list, never assigned by anything.
	set, err := m.Settings()
	if err != nil {
		return err
	}
	if p.ID > 0 && (p.ID == set.BillingFreePlanID || p.ID == set.BillingTrialPlanID) {
		p.PriceRub = 0
		// Enabled means "offered for sale", which a designated plan never is — it is
		// assigned automatically and filtered out of every purchase list. The editor
		// no longer shows the toggle for these, so a stale false would be an invisible
		// switch: it would drop the plan out of the admin's own plan pickers (which
		// filter on enabled) with nothing on screen explaining why.
		p.Enabled = true
	} else if p.PriceRub < 1 {
		return invalidCode("err.priceMustBePositive", "цена должна быть больше 0 — бесплатным тариф становится, когда его выбирают бесплатным или пробным в разделе «Тарификация»")
	}
	if p.SortOrder < 0 {
		p.SortOrder = 0
	}
	// The numeric limits reach this straight off the /v1 API's JSON decode, so they are
	// not bounded by the editor's inputs. A negative period is the dangerous one: it
	// makes expire = now - N, i.e. money taken for a subscription that is already over
	// (EnforceBilling then downgrades the user), and an absurd value overflows the
	// int64(PeriodDays)*86400 into a negative expiry just the same.
	if p.PeriodDays < 0 || p.PeriodDays > maxPlanPeriodDays {
		return invalidCode("err.planPeriodRange", "срок действия: от 0 до {{max}} дней", map[string]any{"max": maxPlanPeriodDays})
	}
	if p.DataLimit < 0 || p.DeviceLimit < 0 || p.SpeedLimit < 0 {
		return invalidCode("err.planLimitsNegative", "лимиты тарифа не могут быть отрицательными")
	}
	if p.DeviceLimit > model.MaxDeviceLimit {
		return invalidCode("err.deviceLimitTooHigh", "лимит устройств не может быть больше {{max}}",
			map[string]any{"max": model.MaxDeviceLimit})
	}
	// "none" and "" both mean the derived default; storing one spelling keeps the
	// "did it change" comparison below honest.
	p.ResetPeriod = strings.TrimSpace(p.ResetPeriod)
	if p.ResetPeriod == "none" {
		p.ResetPeriod = ""
	}
	if p.ResetPeriod != "" && !model.ValidPlanResetPeriod(p.ResetPeriod) {
		return invalidCode("err.badResetPeriod", "неверный период сброса {{value}}", map[string]any{"value": p.ResetPeriod})
	}
	// Access groups the plan grants. Unknown ids are dropped rather than rejected: a
	// group deleted while the editor was open would otherwise make the plan unsavable,
	// and the FK would fail the whole write anyway.
	groups, err := m.store.ExistingGroupIDs(p.GroupIDs)
	if err != nil {
		return err
	}
	p.GroupIDs = groups
	// Whether the grant set moved decides if this save has to regenerate configs.
	// Read before the write, and only for an existing plan — a new one has no users.
	// A failed read counts as moved: the store re-syncs the plan's members either way,
	// so guessing "unchanged" here would leave that with no config to apply it.
	regroup := false
	// Whether the speed cap moved decides if the plan's existing subscribers have to
	// be re-stamped — see below.
	respeed := false
	// Whether the refill cycle moved — same treatment as the cap, see below. The
	// derived default depends on the price and the duration too, so those count.
	recycle := false
	freePlan := p.IsFree() && p.ID != set.BillingTrialPlanID
	if p.ID > 0 {
		prev, err := m.store.GetTariffPlan(p.ID)
		regroup = err != nil || prev == nil || !sameIDs(prev.GroupIDs, p.GroupIDs)
		respeed = err != nil || prev == nil || prev.SpeedLimit != p.SpeedLimit
		recycle = err != nil || prev == nil ||
			planResetPeriod(prev, prev.IsFree() && prev.ID != set.BillingTrialPlanID) != planResetPeriod(p, freePlan)
	}
	if err := m.store.SaveTariffPlan(p); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return invalidCode("err.planSlugTaken", "тариф с таким кодом уже существует")
		}
		return err
	}
	if regroup {
		// The store moved everyone already on the plan into (or out of) those groups;
		// the config has to be regenerated for that to mean anything.
		m.applyAccessChange()
	}
	if respeed {
		// Push the new cap onto everyone already on this plan.
		//
		// The other plan limits (quota, expiry, devices) are deliberately NOT
		// retroactive: rewriting them mid-cycle resets counters and moves the date a
		// subscriber paid for, so they land only when the plan is (re)assigned. A speed
		// cap has none of that baggage — it is a policy value with no side effects —
		// and leaving it non-retroactive is what makes an operator edit a tariff, watch
		// nothing happen, and report the feature as broken.
		n, err := m.store.SetPlanUsersSpeedLimit(p.ID, p.SpeedLimit)
		if err != nil {
			logErr("billing: applying the plan's speed limit to its users failed",
				"plan", p.ID, "err", err)
		} else if n > 0 {
			logInfo("billing: plan speed limit applied to existing users",
				"plan", p.ID, "users", n, "kbps", p.SpeedLimit)
			go m.ApplyShaping()
			m.TriggerUserSync() // nodes shape from the limits in their sync payload
		}
	}
	if recycle {
		// The refill cycle has the same shape as the cap: a policy value with no side
		// effects of its own (the sweep zeroes the counter only when a boundary rolls),
		// so it reaches existing subscribers too — "this tariff refills monthly" is a
		// statement about the tariff, not about the next purchase. Anchored at now: for
		// a calendar cycle the next refill lands on the next boundary either way, and
		// a rolling days:N restarts its count.
		period := planResetPeriod(p, freePlan)
		n, err := m.store.SetPlanUsersResetPeriod(p.ID, period, time.Now().Unix())
		if err != nil {
			logErr("billing: applying the plan's reset period to its users failed",
				"plan", p.ID, "err", err)
		} else if n > 0 {
			logInfo("billing: plan reset period applied to existing users",
				"plan", p.ID, "users", n, "period", period)
		}
	}
	return nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func slugifyPlan(name string) string {
	s := strings.ToLower(name)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "plan"
	}
	return out
}

func (m *Manager) DeleteTariffPlan(id int64) error {
	set, err := m.Settings()
	if err != nil {
		return err
	}
	if set.BillingFreePlanID == id || set.BillingTrialPlanID == id {
		return invalidCode("err.planUsedInBilling", "тариф указан в настройках биллинга — сначала выберите другой")
	}
	n, err := m.store.CountUsersOnPlan(id)
	if err != nil {
		return err
	}
	if n > 0 {
		return invalidCode("err.planHasUsers", "тариф назначен {{count}} пользователям — сначала смените им тариф", map[string]any{"count": n})
	}
	// Orders still awaiting payment pin the plan too. The documented way to retire a
	// plan is "migrate its users, then delete", which is exactly when in-flight orders
	// exist — and every order read joins tariff_plans, so deleting it now would make
	// those orders unresolvable by the webhook, the poller and the operator alike.
	pending, err := m.store.CountPendingOrdersForPlan(id)
	if err != nil {
		return err
	}
	if pending > 0 {
		return invalidCode("err.planHasPendingOrders",
			"по тарифу есть {{count}} неоплаченных заказов — дождитесь оплаты или отмените их",
			map[string]any{"count": pending})
	}
	return m.store.DeleteTariffPlan(id)
}

// MigratePlanUsers moves every user currently on fromPlanID to toPlanID (applying
// the target plan's limits and period). Used when retiring a plan. Returns how many
// users were moved.
func (m *Manager) MigratePlanUsers(ctx context.Context, fromPlanID, toPlanID int64) (int, error) {
	if fromPlanID == toPlanID {
		return 0, invalidCode("err.pickAnotherPlanToMigrate", "выберите другой тариф для перевода")
	}
	if _, err := m.store.GetTariffPlan(toPlanID); err != nil {
		return 0, invalidCode("err.targetPlanNotFound", "целевой тариф не найден")
	}
	ids, err := m.store.UserIDsOnPlan(fromPlanID)
	if err != nil {
		return 0, err
	}
	migrated := 0
	for _, id := range ids {
		if err := m.ApplyPlanToUser(ctx, id, toPlanID, false); err != nil {
			logErr("billing: plan migration failed", "user", id, "from_plan", fromPlanID, "to_plan", toPlanID, "err", err)
			continue
		}
		migrated++
	}
	logInfo("billing: migrated users between plans", "migrated", migrated, "total", len(ids), "from_plan", fromPlanID, "to_plan", toPlanID)
	return migrated, nil
}

func (m *Manager) SaveBillingSettings(st *model.Settings) error {
	// The two roles must be different plans. Sharing one looks harmless in the UI but
	// is a dead end for every self-registered user: planWriteFor treats the trial as
	// paid-shaped (it must expire), while EnforceBilling refuses to downgrade anyone
	// already on the free plan — so the trial expires and nothing ever rescues them.
	if st.BillingFreePlanID != 0 && st.BillingFreePlanID == st.BillingTrialPlanID {
		return invalidCode("err.freeAndTrialMustDiffer", "бесплатный и пробный тарифы должны быть разными: иначе после окончания пробного периода пользователю некуда переходить")
	}
	if err := m.store.SetBillingSettings(st); err != nil {
		return err
	}
	// Designating a plan as the free or trial one MAKES it free: a paid plan left at
	// its price here would be handed out for nothing (registration and the expiry
	// downgrade never charge), and IsFree would still call it paid — so it would also
	// stay on sale. Zero the price to match what the designation already means.
	ctx := context.Background()
	for _, id := range []int64{st.BillingFreePlanID, st.BillingTrialPlanID} {
		if id == 0 {
			continue
		}
		plan, err := m.store.GetTariffPlan(id)
		if err != nil || plan == nil || (plan.PriceRub == 0 && plan.Enabled) {
			continue
		}
		wasPaid := plan.PriceRub > 0
		plan.PriceRub = 0
		plan.Enabled = true // see SaveTariffPlan: the toggle is gone for designated plans
		if err := m.store.SaveTariffPlan(plan); err != nil {
			// Reported rather than swallowed: the settings write above has already
			// committed, so a silent failure leaves the panel handing out a plan it
			// still considers paid — free to every registrant AND still on sale.
			return fmt.Errorf("could not make the %q plan free: %w", plan.Name, err)
		}
		logInfo("billing: designated plan is now free and active", "plan", plan.Name, "id", id)
		if !wasPaid {
			continue
		}
		// The plan row is free now, but everyone already on it still carries the paid
		// shape it was bought under: an expiry in the future and no refill cycle. Left
		// alone they expire and are then stuck — EnforceBilling skips users already on
		// the free plan, and a free plan cannot be renewed — so re-apply the plan to
		// rewrite those rows with the semantics it now has.
		m.reapplyPlanToItsUsers(ctx, id)
	}
	return nil
}

// reapplyPlanToItsUsers re-runs the plan assignment for every user currently on it,
// so their row reflects the plan's present terms. Best-effort per user: one failure
// must not strand the rest.
func (m *Manager) reapplyPlanToItsUsers(ctx context.Context, planID int64) {
	ids, err := m.store.UserIDsOnPlan(planID)
	if err != nil {
		logErr("billing: listing users of a newly designated plan failed", "plan", planID, "err", err)
		return
	}
	var done int
	for _, uid := range ids {
		if err := m.applyPlan(ctx, uid, planID, false, ""); err != nil {
			logErr("billing: re-applying a newly designated plan failed", "user", uid, "plan", planID, "err", err)
			continue
		}
		done++
	}
	if done > 0 {
		logInfo("billing: rewrote users onto the newly free plan", "users", done, "plan", planID)
	}
}

// CreateRegisteredUser creates an active user from self-registration (trial/free/
// plain per billing config), links nothing itself, and alerts the admin chats. Used
// by the open and invite modes; moderation instead goes through RequestRegistration.
func (m *Manager) CreateRegisteredUser(ctx context.Context, name string) (*model.User, error) {
	u, err := m.createRegisteredUser(name)
	if err != nil || u == nil {
		return u, err
	}
	plan := m.PlanName(u.PlanID)
	lang := m.botLang()
	m.notifyAdminEvent(model.AdminEventRegistered,
		i18n.T(lang, "notify.registered", escHTML(u.Name))+planLine(lang, plan))
	m.audit(ctx, u.ID, model.EventUserRegistered, map[string]any{"plan": plan})
	m.EmitWebhook(model.WebhookUserRegistered, userEventData(*u))
	return u, nil
}

func planLine(lang i18n.Lang, plan string) string {
	if plan == "" {
		return ""
	}
	return i18n.T(lang, "notify.planLine", escHTML(plan))
}

// RequestRegistration records a moderated signup: no user is created — the request
// is held for an admin decision. Returns ok=false when the chat already has a pending
// request (the caller then tells the applicant it's still under review). The admin is
// prompted with approve/reject buttons (or a plain alert when the admin bot is off).
func (m *Manager) RequestRegistration(ctx context.Context, chatID int64, name string) (ok bool, err error) {
	name = truncateName(strings.TrimSpace(name))
	if name == "" {
		name = fmt.Sprintf("tg-%d", chatID)
	}
	req, err := m.store.CreateRegistrationRequest(chatID, name, time.Now().Unix())
	if errors.Is(err, store.ErrRegistrationPending) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Best-effort admin-bot ping with approve/reject buttons. The panel's sign-up
	// requests tab is the authoritative surface regardless (and the only one when
	// the admin bot is off or its registration notifications are disabled).
	m.notifyModeration(req.ID, req.Name, "")
	return true, nil
}

// RegistrationPending reports whether a chat has a signup awaiting a decision.
func (m *Manager) RegistrationPending(chatID int64) bool {
	r, err := m.store.GetRegistrationRequestByChat(chatID)
	return err == nil && r != nil
}

// ListRegistrationRequests returns the pending moderated signups (for the panel).
func (m *Manager) ListRegistrationRequests() ([]model.RegistrationRequest, error) {
	return m.store.ListRegistrationRequests()
}

// ApproveRegistrationRequest turns a pending request into a real (active) user: it
// creates the account, links the applicant's chat, drops the request and notifies
// them. The request is claimed atomically first, so concurrent approvals (or an
// approve racing a reject) resolve to a single winner — no duplicate account.
func (m *Manager) ApproveRegistrationRequest(ctx context.Context, reqID int64) error {
	req, err := m.store.GetRegistrationRequest(reqID)
	if err != nil {
		return invalidCode("err.requestNotFound", "заявка не найдена")
	}
	claimed, err := m.store.ClaimRegistrationRequest(reqID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil // another admin already decided this request
	}
	// If the chat got linked to an account in the meantime (e.g. via a panel link
	// code), don't mint a duplicate — just let the applicant know they're set.
	if existing, _ := m.store.GetUserByTelegramChatID(req.ChatID); existing != nil {
		m.notifyRegistrationDecision(req.ChatID, "notify.regAlreadyLinked")
		return nil
	}
	u, err := m.createRegisteredUser(req.Name)
	if err != nil {
		// Creation failed after the request was claimed — put the request back so it's
		// retryable instead of vanishing (the applicant keeps waiting otherwise).
		_, _ = m.store.CreateRegistrationRequest(req.ChatID, req.Name, req.CreatedAt)
		return err
	}
	if err := m.store.SetUserTelegramChat(u.ID, req.ChatID); err != nil {
		// Account created but the chat couldn't be linked: drop the orphan and restore
		// the request rather than leave an unreachable active account behind.
		_ = m.store.DeleteUser(u.ID)
		_, _ = m.store.CreateRegistrationRequest(req.ChatID, req.Name, req.CreatedAt)
		return err
	}
	plan := m.PlanName(u.PlanID)
	m.audit(ctx, u.ID, model.EventUserRegistered, map[string]any{"plan": plan, "moderation": true})
	m.EmitWebhook(model.WebhookUserRegistered, userEventData(*u))
	// Gated with the other user-facing notices: an operator who switched them all off
	// should not still have the bot writing to people.
	m.notifyRegistrationDecision(req.ChatID, "notify.regApproved")
	return nil
}

// RejectRegistrationRequest declines a pending request: it's dropped and the
// applicant is told. No user was ever created. Claimed atomically so it can't race
// an approval into a contradictory outcome.
func (m *Manager) RejectRegistrationRequest(ctx context.Context, reqID int64) error {
	req, err := m.store.GetRegistrationRequest(reqID)
	if err != nil {
		return invalidCode("err.requestNotFound", "заявка не найдена")
	}
	claimed, err := m.store.ClaimRegistrationRequest(reqID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil // another admin already decided this request
	}
	m.notifyRegistrationDecision(req.ChatID, "notify.regRejected")
	return nil
}

// createRegisteredUser is the registration body: trial → free → plain user.
func (m *Manager) createRegisteredUser(name string) (*model.User, error) {
	// Self-registration name comes from the Telegram display name — bound its length
	// (truncate rather than reject) so it can't bloat the DB / config unboundedly.
	name = truncateName(name)
	if name == "" {
		return nil, invalidCode("err.nameRequired", "укажите имя")
	}
	set, err := m.Settings()
	if err != nil {
		return nil, err
	}
	if !set.BillingEnabled {
		return m.createUser(name, 0, 0)
	}
	now := time.Now().Unix()
	// The trial's length is the trial plan's own period — there is no separate
	// "trial days" setting to disagree with it. A trial plan without a period would
	// never expire, so it falls through to the free plan instead.
	if set.BillingTrialPlanID > 0 {
		plan, err := m.store.GetTariffPlan(set.BillingTrialPlanID)
		// Not gated on plan.Enabled: designating a plan as the trial IS the on switch
		// (clear it in "Pricing" to stop granting trials), and the editor no longer
		// offers the toggle for designated plans — so reading it here would be an
		// invisible second switch that silently disables registration trials.
		if err == nil && plan != nil && plan.PeriodDays > 0 {
			u, err := m.createBareUser(name)
			if err != nil {
				return nil, err
			}
			expire := now + int64(plan.PeriodDays)*86400
			w := planLimits(u.ID, plan, expire, false, now)
			w.TrialUsed = true
			if err := m.store.ApplyUserPlan(w); err != nil {
				return nil, err
			}
			logInfo("user registered with trial plan", "user", u.ID, "plan", plan.Name, "days", plan.PeriodDays)
			m.TriggerUserSync()
			return m.store.GetUser(u.ID)
		}
	}
	if set.BillingFreePlanID > 0 {
		plan, err := m.store.GetTariffPlan(set.BillingFreePlanID)
		if err == nil && plan != nil {
			u, err := m.createBareUser(name)
			if err != nil {
				return nil, err
			}
			if err := m.store.ApplyUserPlan(planLimits(u.ID, plan, 0, plan.IsFree(), now)); err != nil {
				return nil, err
			}
			logInfo("user registered with free plan", "user", u.ID, "plan", plan.Name)
			m.TriggerUserSync()
			return m.store.GetUser(u.ID)
		}
	}
	return m.createUser(name, 0, 0)
}

func (m *Manager) createBareUser(name string) (*model.User, error) {
	password, err := auth.RandomPassword()
	if err != nil {
		return nil, err
	}
	subToken, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	return m.store.CreateUser(name, uuid.NewString(), password, subToken, 0, 0, 0)
}

// planLimits computes the quota/expiry/reset columns a plan implies, without
// writing them. Pure on purpose: the caller commits the result through
// store.ApplyUserPlan (or, for a purchase, together with the order's paid claim),
// which is what keeps the several columns a plan touches from landing separately.
// PlanID is filled in here; TrialUsed is the caller's to set.
func planLimits(userID int64, plan *model.TariffPlan, expireAt int64, freeReset bool, now int64) store.UserPlanWrite {
	return store.UserPlanWrite{
		UserID:      userID,
		DataLimit:   plan.DataLimit,
		ExpireAt:    expireAt,
		DeviceLimit: plan.DeviceLimit,
		SpeedLimit:  plan.SpeedLimit,
		ResetPeriod: planResetPeriod(plan, freeReset),
		ResetAnchor: now,
		PlanID:      plan.ID,
		GroupIDs:    plan.GroupIDs,
	}
}

// planResetPeriod is the users.reset_period a plan implies. The plan's own cycle
// wins when it has one; otherwise the derived default: a free plan refills every
// plan duration (a rolling N-day cycle, not a calendar month), a paid one runs its
// quota over the whole period bought. No cycle at all without a quota to refill —
// an unlimited plan on "monthly" would only zero counters and write an audit row
// every month for nothing — and a free plan with no duration (PeriodDays 0) never
// resets either: its quota is one-time.
func planResetPeriod(plan *model.TariffPlan, freeReset bool) string {
	switch {
	case plan.DataLimit <= 0:
		return "none"
	case plan.ResetPeriod != "":
		return plan.ResetPeriod
	case freeReset && plan.PeriodDays > 0:
		return fmt.Sprintf("days:%d", plan.PeriodDays)
	}
	return "none"
}

// planGroupsChanged reports whether writing w actually moves the user's group
// membership — the only case that needs the full reconcile a gate change costs (a
// renewal of the same plan doesn't). It asks the question the write will answer:
// which rows does it DELETE (plan-owned, no longer granted) and which does it INSERT
// (granted, not a member by any route yet). Comparing the plan-owned set with the new
// one alone would keep reporting a change for a user whose hand-assigned membership
// the plan also grants — that row is never converted, so every renewal would restart
// Xray for nothing. On a read error it says yes: a needless restart is cheaper than a
// user left with the lanes of the plan they just left.
func (m *Manager) planGroupsChanged(w store.UserPlanWrite) bool {
	owned, err := m.store.UserPlanGroups(w.UserID)
	if err != nil {
		return true
	}
	granted := make(map[int64]bool, len(w.GroupIDs))
	for _, id := range w.GroupIDs {
		granted[id] = true
	}
	for _, id := range owned {
		if !granted[id] {
			return true // this membership is about to be taken back
		}
	}
	if len(granted) == 0 {
		return false
	}
	current, err := m.store.GroupsForUser(w.UserID)
	if err != nil {
		return true
	}
	member := make(map[int64]bool, len(current))
	for _, g := range current {
		member[g.ID] = true
	}
	for id := range granted {
		if !member[id] {
			return true // a membership is about to be added
		}
	}
	return false
}

// sameIDs compares two id lists as sets (order and duplicates don't matter).
func sameIDs(a, b []int64) bool {
	sa := make(map[int64]bool, len(a))
	for _, id := range a {
		sa[id] = true
	}
	sb := make(map[int64]bool, len(b))
	for _, id := range b {
		sb[id] = true
	}
	if len(sa) != len(sb) {
		return false
	}
	for id := range sa {
		if !sb[id] {
			return false
		}
	}
	return true
}

// afterPlanWrite propagates a committed plan assignment. Limits alone are a live
// user-sync (no restart); a change in the groups the plan grants is a change to WHICH
// connections carry the user's credential, which only config generation can apply —
// so that one reconciles and wakes the nodes, exactly like a group edit.
func (m *Manager) afterPlanWrite(groupsChanged bool) {
	if groupsChanged {
		m.applyAccessChange()
		return
	}
	m.TriggerUserSync()
}

// ApplyPlanToUser assigns a tariff and updates limits. extendFromCurrent stacks paid time.
// planID 0 switches to manual mode: clears plan link and resets limits to unlimited.
func (m *Manager) ApplyPlanToUser(ctx context.Context, userID int64, planID int64, extendFromCurrent bool) error {
	return m.applyPlan(ctx, userID, planID, extendFromCurrent, model.EventPlanChanged)
}

// applyPlan is the body of ApplyPlanToUser, parameterized by the audit action to
// record. Callers that own a more specific story about the change pass their own
// action (an expiry downgrade) or "" to stay silent and log the event themselves
// (a cancellation, which would otherwise read as a plain switch to the free plan).
func (m *Manager) applyPlan(ctx context.Context, userID int64, planID int64, extendFromCurrent bool, action string) error {
	// Serialize the expire_at read-modify-write below against concurrent confirmers.
	m.applyPlanMu.Lock()
	defer m.applyPlanMu.Unlock()
	u, err := m.store.GetUser(userID)
	if err != nil {
		return err
	}
	prevPlan := m.PlanName(u.PlanID)
	w, planName, err := m.planWriteFor(*u, planID, extendFromCurrent, false)
	if err != nil {
		return err
	}
	groupsChanged := m.planGroupsChanged(w)
	if err := m.store.ApplyUserPlan(w); err != nil {
		return err
	}
	m.afterPlanWrite(groupsChanged)
	m.auditPlan(ctx, userID, u.Name, action, prevPlan, planName, w.ExpireAt)
	return nil
}

// planWriteFor resolves "give this user this plan" into the exact row the users
// table should end up with, and the plan's display name for the audit trail. It
// reads (the tariff, the settings) but writes nothing, so both callers — a plain
// assignment and a payment confirmation — can commit the result in whatever
// transaction they need. Callers must hold applyPlanMu: the expiry it computes is a
// read-modify-write of the user's current expire_at.
//
// planID 0 means manual mode: no plan link, no limits, no reset cycle.
// paidPeriod marks a period the user actually bought (the purchase path), which is
// what entitles them to a fresh quota on a plan they already hold — see the ResetUsage
// decision below.
func (m *Manager) planWriteFor(u model.User, planID int64, extendFromCurrent, paidPeriod bool) (store.UserPlanWrite, string, error) {
	now := time.Now().Unix()
	if planID == 0 {
		return store.UserPlanWrite{
			UserID:      u.ID,
			ResetPeriod: "none",
			ResetAnchor: now,
			TrialUsed:   u.TrialUsed,
		}, "", nil
	}
	plan, err := m.store.GetTariffPlan(planID)
	if err != nil {
		return store.UserPlanWrite{}, "", err
	}
	set, err := m.Settings()
	if err != nil {
		return store.UserPlanWrite{}, "", err
	}
	// The designated trial plan is a zero-price template that still EXPIRES when
	// assigned (period-limited proba), so it is NOT treated as a free plan here
	// even though its price is 0 — a manual assignment gives period_days of access,
	// then EnforceBilling downgrades it to the free plan, same as the trial flow.
	freePlan := plan.IsFree() && plan.ID != set.BillingTrialPlanID
	var expire int64
	if !freePlan && plan.PeriodDays > 0 {
		base := now
		if extendFromCurrent && u.ExpireAt > now {
			base = u.ExpireAt
		}
		expire = base + int64(plan.PeriodDays)*86400
	}
	w := planLimits(u.ID, plan, expire, freePlan, now)
	w.TrialUsed = u.TrialUsed
	// A different plan means a different quota, so the counter starts over. Without
	// this the expiry path is a trap: EnforceBilling hands the user the free plan with
	// a fresh 30-day cycle, the 20 GB they spent on the paid one stays on the counter,
	// and a 1 GB allowance is over budget the moment it is granted — the user is cut
	// off until the cycle rolls, a month later.
	//
	// Only on a real change of plan, or on a period the user PAID for. An operator
	// re-assigning the same plan tops up the time left rather than starting a period,
	// so it keeps the counter it was running — re-assigning must not silently hand out
	// a free refill (see TestSamePlanKeepsUsage). Manual mode (planID 0) grants no
	// quota at all and is handled above.
	//
	// A purchase is different, and paidPeriod marks it: by default a paid plan carries
	// no rolling refill (planResetPeriod gives it "none"), so its quota is tied to the
	// period the money buys. Without this the one path that never refills is the one
	// the user pays for — burn the quota mid-period, press "продлить", and the payment
	// buys fresh time on a spent counter, leaving WorkingUsers to filter the user out
	// (used >= data_limit) with the money taken. A paid plan WITH its own cycle
	// (monthly, say) still gets the fresh counter on purchase: money taken must mean
	// access now, not on the 1st. A free plan is excluded: its days:N cycle already
	// refills it, and nothing is bought.
	if u.PlanID != plan.ID || (paidPeriod && !freePlan && plan.DataLimit > 0) {
		w.ResetUsage = true
		w.LastUp, w.LastDown = m.liveCounter(u.ID)
	}
	return w, plan.Name, nil
}

// auditPlan records a plan change. An empty action means the caller logs its own
// event instead (see applyPlan). The name is passed in because applyPlan already
// read the user — re-reading it here would add a serialized DB round-trip while the
// global applyPlanMu is held.
func (m *Manager) auditPlan(ctx context.Context, userID int64, userName, action, prevPlan, newPlan string, expire int64) {
	if action == "" {
		return
	}
	m.auditNamed(ctx, userID, userName, action, map[string]any{
		"plan": newPlan, "prev_plan": prevPlan, "expire_at": expire,
	})
}

// isPlanRenewal reports whether applying planID to the user is a renewal of their
// currently-active paid plan — the only case where paid time should extend from the
// current expiry instead of starting from now (buying from trial/free/expired must
// start fresh, not inherit the remaining time).
func (m *Manager) isPlanRenewal(userID, planID int64) bool {
	u, err := m.store.GetUser(userID)
	if err != nil {
		return false
	}
	return m.isPlanRenewalFor(*u, planID)
}

func (m *Manager) isPlanRenewalFor(u model.User, planID int64) bool {
	ap := m.ActivePaidPlan(u)
	return ap != nil && ap.ID == planID
}

// confirmOrderPaid is the one place an order becomes paid. It claims the
// pending→paid transition and grants the plan in a single transaction, and reports
// whether this caller won the claim (false = someone else already confirmed it, and
// nothing was applied a second time).
//
// Both confirmation paths — the provider webhook/poll and the operator's manual
// confirm — go through here, because both used to do it the same unsafe way: claim
// first, grant after. Anything that killed the process in between took the money and
// left no trace that the plan was owed, since every retry path looks for pending
// orders and the claim had already cleared that flag.
func (m *Manager) confirmOrderPaid(order *model.PaymentOrder, paidAt int64) (bool, error) {
	// Held across the read and the commit: the expiry being computed extends the
	// user's current one, so a concurrent confirmer must not read the same baseline.
	m.applyPlanMu.Lock()
	defer m.applyPlanMu.Unlock()
	u, err := m.store.GetUser(order.UserID)
	if err != nil {
		return false, err
	}
	// Extend from the current expiry only for a renewal of the active paid plan;
	// buying from trial/free/expired starts from now (no inherited time).
	w, _, err := m.planWriteFor(*u, order.PlanID, m.isPlanRenewalFor(*u, order.PlanID), true)
	if err != nil {
		return false, err
	}
	groupsChanged := m.planGroupsChanged(w)
	claimed, err := m.store.ConfirmPaymentOrder(order.ID, paidAt, w)
	if err != nil || !claimed {
		return false, err
	}
	m.afterPlanWrite(groupsChanged)
	return true, nil
}

// ActivePaidPlan returns the user's current tariff when it's a paid plan that is
// still active (expiry in the future), else nil. This is the "locked" state where
// only renewal or cancellation is allowed — not switching to another plan. A trial
// or free plan (price 0) never counts, so upgrading from those stays open.
func (m *Manager) ActivePaidPlan(u model.User) *model.TariffPlan {
	if u.PlanID == 0 {
		return nil
	}
	plan, err := m.store.GetTariffPlan(u.PlanID)
	if err != nil || plan == nil || plan.IsFree() {
		return nil
	}
	// A paid plan sold with no duration ("бессрочно", period_days 0) never sets an
	// expiry, so expire_at stays 0 — the same value an unlimited free account carries.
	// Treating that as "expired" made a lifetime subscriber look like they had no plan
	// at all: the purchase list offered them the plan they already own with no cancel
	// button, and buying it again wrote nothing while the order was marked paid.
	if plan.PeriodDays > 0 && u.ExpireAt <= time.Now().Unix() {
		return nil
	}
	return plan
}

// CancelUserPlan cancels a paid subscription immediately: the user is moved to the
// free plan right away (losing any remaining paid time), matching what EnforceBilling
// does on expiry — but on demand. With no free plan configured, access is ended
// instead (plan cleared, expired now). The consumed-trial flag is preserved so
// cancelling can't reopen a fresh trial.
func (m *Manager) CancelUserPlan(ctx context.Context, userID int64) error {
	set, err := m.Settings()
	if err != nil {
		return err
	}
	// The plan being cancelled, captured before it's replaced.
	cancelled := ""
	if u, err := m.store.GetUser(userID); err == nil {
		cancelled = m.PlanName(u.PlanID)
	}
	if set.BillingFreePlanID != 0 {
		if free, err := m.store.GetTariffPlan(set.BillingFreePlanID); err == nil && free != nil {
			// Audited as a cancellation, not as the plan switch it's implemented as.
			if err := m.applyPlan(ctx, userID, free.ID, false, ""); err != nil {
				return err
			}
			m.audit(ctx, userID, model.EventPlanCancelled, map[string]any{
				"plan": cancelled, "moved_to": free.Name,
			})
			return nil
		}
	}
	// No free plan: end the subscription now — clear the plan and expire immediately.
	// Under applyPlanMu like every other plan write: this reads the user's limits and
	// writes them back, so without it a renewal confirming concurrently would be
	// overwritten with the pre-purchase limits and expire_at = now — a paid period
	// silently eaten.
	m.applyPlanMu.Lock()
	defer m.applyPlanMu.Unlock()
	u, err := m.store.GetUser(userID)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	// No GroupIDs: the cancelled plan's groups go with it, like any other move off a
	// plan (a hand-assigned group stays — it was never the plan's to take).
	w := store.UserPlanWrite{
		UserID:      userID,
		DataLimit:   u.DataLimit,
		ExpireAt:    now, // expire immediately: there is no free plan to fall back to
		DeviceLimit: u.DeviceLimit,
		ResetPeriod: "none",
		ResetAnchor: now,
		TrialUsed:   u.TrialUsed,
	}
	groupsChanged := m.planGroupsChanged(w)
	if err := m.store.ApplyUserPlan(w); err != nil {
		return err
	}
	m.afterPlanWrite(groupsChanged)
	m.audit(ctx, userID, model.EventPlanCancelled, map[string]any{"plan": cancelled})
	return nil
}

// EnforceBilling downgrades users whose paid/trial period ended to the free plan.
// It runs off the background poller, so its audit rows are attributed to the system.
func (m *Manager) EnforceBilling(now int64) error {
	set, err := m.Settings()
	if err != nil || !set.BillingEnabled || set.BillingFreePlanID == 0 {
		return nil
	}
	free, err := m.store.GetTariffPlan(set.BillingFreePlanID)
	if err != nil {
		return nil
	}
	users, err := m.store.UsersWithExpiredPlan(now)
	if err != nil {
		return err
	}
	ctx := context.Background()
	for _, u := range users {
		if u.PlanID == free.ID {
			continue
		}
		if err := m.applyPlan(ctx, u.ID, free.ID, false, model.EventPlanDowngraded); err != nil {
			logErr("billing: downgrade to free failed", "user", u.ID, "err", err)
			continue
		}
		logInfo("billing: user downgraded to free plan after expiry", "user", u.ID)
	}
	return nil
}

// RequestPlanPayment opens a pending manual order for a paid plan and returns the
// payment instructions. To keep a spammed "Pay" button from piling up duplicate
// orders (and admin pings), it reuses the user's latest still-pending manual order
// for the same plan instead of creating another.
func (m *Manager) RequestPlanPayment(ctx context.Context, lang i18n.Lang, userID, planID int64) (*model.PaymentOrder, string, error) {
	plan, err := m.store.GetTariffPlan(planID)
	if err != nil {
		return nil, "", invalidCode("err.planNotFound", "тариф не найден")
	}
	if plan.IsFree() {
		return nil, "", invalidCode("err.planIsFree", "этот тариф бесплатный")
	}
	// Same rules as the automatic path: block switching (and buying a disabled plan)
	// while a paid one is active — but let an existing subscriber renew the plan
	// they're already on, even if it's since been disabled (grandfathering).
	if u, err := m.store.GetUser(userID); err == nil && u.PlanID != planID {
		if !plan.Enabled {
			return nil, "", invalidCode("err.planUnavailable", "тариф недоступен")
		}
		if cur := m.ActivePaidPlan(*u); cur != nil {
			return nil, "", invalidCode("err.activeSubscription", "у вас активна подписка «{{plan}}» — сначала отмените её, чтобы сменить тариф", map[string]any{"plan": cur.Name})
		}
	}
	set, _ := m.Settings()
	if existing, err := m.store.LatestPendingManualOrder(userID, planID); err == nil && existing != nil {
		return existing, manualOrderMessage(lang, existing, plan, set), nil // reuse, no new order/notification
	}
	order, err := m.store.CreatePaymentOrder(userID, planID, plan.PriceRub)
	if err != nil {
		return nil, "", err
	}
	m.notifyAdminEvent(model.AdminEventPayment, i18n.T(m.botLang(), "notify.manualOrder",
		order.ID, escHTML(order.UserName), escHTML(plan.Name), plan.PriceRub))
	m.audit(ctx, userID, model.EventPaymentCreated, map[string]any{
		"order_id": order.ID, "plan": plan.Name, "amount_rub": plan.PriceRub, "provider": "manual",
	})
	m.EmitWebhook(model.WebhookPaymentCreated, order)
	return order, manualOrderMessage(lang, order, plan, set), nil
}

// manualOrderMessage builds the user-facing manual-payment instructions for an
// order: amount, the operator's payment note, and the order number to quote in the
// transfer comment.
func manualOrderMessage(lang i18n.Lang, order *model.PaymentOrder, plan *model.TariffPlan, set *model.Settings) string {
	msg := i18n.T(lang, "order.head", order.ID, plan.Name, plan.PriceRub)
	// The operator's own payment note is their words, in whatever language they wrote
	// it — passed through untouched.
	if set != nil && strings.TrimSpace(set.BillingPaymentNote) != "" {
		msg += "\n\n" + strings.TrimSpace(set.BillingPaymentNote)
	}
	msg += i18n.T(lang, "order.comment", order.ID)
	msg += i18n.T(lang, "order.afterConfirm")
	return msg
}

// ConfirmPayment marks an order paid and applies the plan. Idempotent: the atomic
// pending→paid claim means a double-submit / retry (or an overlap with the provider
// webhook on a provider order) applies the plan at most once.
func (m *Manager) ConfirmPayment(ctx context.Context, orderID int64) error {
	order, err := m.store.GetPaymentOrder(orderID)
	if err != nil {
		return err
	}
	if order.Status != "pending" {
		return invalidCode("err.orderAlreadyHandled", "заказ уже обработан")
	}
	now := time.Now().Unix()
	// The claim and the plan land together — see confirmOrderPaid. Audited as the
	// payment below rather than as a bare plan switch: one purchase is one event,
	// and payment.paid already names the plan.
	claimed, err := m.confirmOrderPaid(order, now)
	if err != nil {
		return err
	}
	if !claimed {
		return invalidCode("err.orderAlreadyHandled", "заказ уже обработан")
	}
	logInfo("billing: order confirmed", "order", orderID, "user", order.UserID, "plan", order.PlanID)
	order.Status, order.PaidAt = "paid", now
	m.audit(ctx, order.UserID, model.EventPaymentPaid, map[string]any{
		"order_id": order.ID, "plan": order.PlanName, "amount_rub": order.AmountRub, "provider": "manual",
	})
	m.EmitWebhook(model.WebhookPaymentPaid, order)
	return nil
}

func (m *Manager) CancelPayment(ctx context.Context, orderID int64) error {
	// Only a PENDING order may be cancelled. The unconditional write this used to do
	// would happily rewrite an order that was already paid — zeroing paid_at (the
	// payment vanishes from the revenue reports), emitting a payment.cancelled webhook
	// for money that was actually taken, and leaving the user holding the plan it
	// bought. It also races a confirming webhook: the admin cancels what the UI still
	// shows as pending at the moment the claim commits.
	cancelled, err := m.store.CancelPaymentOrderIfPending(orderID)
	if err != nil {
		return err
	}
	if !cancelled {
		return invalidCode("err.orderNotPending", "заказ уже оплачен или отменён — обновите список")
	}
	// Best-effort payload enrichment: re-read the (now cancelled) order.
	if order, err := m.store.GetPaymentOrder(orderID); err == nil {
		m.audit(ctx, order.UserID, model.EventPaymentCancelled, map[string]any{
			"order_id": order.ID, "plan": order.PlanName, "amount_rub": order.AmountRub,
		})
		m.EmitWebhook(model.WebhookPaymentCancelled, order)
	} else {
		m.EmitWebhook(model.WebhookPaymentCancelled, map[string]any{"id": orderID})
	}
	return nil
}

func (m *Manager) ListPaymentOrders(status string) ([]model.PaymentOrder, error) {
	return m.store.ListPaymentOrders(status, 100)
}

// PaymentStats assembles the revenue dashboard: all-time and per-provider paid
// totals, revenue since local midnight / the 1st of the month, and the pending
// backlog. Day/month boundaries use the operator's configured timezone.
func (m *Manager) PaymentStats() (*model.PaymentStats, error) {
	byProvider, err := m.store.PaidByProvider()
	if err != nil {
		return nil, err
	}
	var total, count int
	for _, p := range byProvider {
		total += p.Sum
		count += p.Count
	}
	now := time.Now().In(m.loc())
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, m.loc()).Unix()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, m.loc()).Unix()
	today, err := m.store.PaidSumSince(dayStart)
	if err != nil {
		return nil, err
	}
	month, err := m.store.PaidSumSince(monthStart)
	if err != nil {
		return nil, err
	}
	pendingCount, pendingSum, err := m.store.PendingTotals()
	if err != nil {
		return nil, err
	}
	return &model.PaymentStats{
		TotalPaid:    total,
		PaidCount:    count,
		EarnedToday:  today,
		EarnedMonth:  month,
		PendingCount: pendingCount,
		PendingSum:   pendingSum,
		ByProvider:   byProvider,
	}, nil
}

func (m *Manager) PlanName(planID int64) string {
	if planID == 0 {
		return ""
	}
	p, err := m.store.GetTariffPlan(planID)
	if err != nil {
		return ""
	}
	return p.Name
}
