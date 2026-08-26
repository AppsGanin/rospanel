package telegram

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// menuHeader is the main-menu caption.
func menuHeader(lang i18n.Lang) string { return i18n.T(lang, "admin.menuHeader") }

// accessDenied is shown to a chat that isn't linked.
func accessDenied(lang i18n.Lang) string { return i18n.T(lang, "admin.accessDenied") }

// mainMenuRows is the top-level inline keyboard.
func mainMenuRows(lang i18n.Lang) [][]InlineButton {
	return [][]InlineButton{
		{{Text: i18n.T(lang, "admin.btnUsers"), CallbackData: "users:0"}},
		{{Text: i18n.T(lang, "admin.btnBackup"), CallbackData: "backup"}},
	}
}

// backToMenu is the single "back to menu" row reused by leaf views.
func backToMenu(lang i18n.Lang) [][]InlineButton {
	return [][]InlineButton{{{Text: i18n.T(lang, "admin.btnMenu"), CallbackData: "menu"}}}
}

// esc HTML-escapes dynamic text so it's safe inside an HTML-parse-mode message.
func esc(s string) string { return html.EscapeString(s) }

// splitCmd extracts the lowercased command (without a "@botname" suffix) and the
// remaining whitespace-separated arguments — used only for "/start <code>".
func splitCmd(text string) (cmd string, args []string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", nil
	}
	cmd = fields[0]
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	return strings.ToLower(cmd), fields[1:]
}

// atoiOr parses s as an int, returning def on failure.
func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

// joinIDs renders chat IDs as a comma-separated string for the tg_chat_ids column.
func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// formatBytes renders a byte count in binary units (KB = 1024 B).
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// statusEmoji is the compact status indicator used on list buttons.
func statusEmoji(status string) string {
	switch status {
	case "active":
		return "✅"
	case "disabled":
		return "⛔"
	case "expired":
		return "⌛"
	case "limited":
		return "📵"
	case "device_limited":
		return "📱"
	default:
		return "•"
	}
}

// statusLabel is the status with a word, used on the user card.
func statusLabel(status string, lang i18n.Lang) string {
	switch status {
	case "active":
		return i18n.T(lang, "admin.stActive")
	case "disabled":
		return i18n.T(lang, "admin.stDisabled")
	case "expired":
		return i18n.T(lang, "admin.stExpired")
	case "limited":
		return i18n.T(lang, "admin.stLimited")
	case "device_limited":
		return i18n.T(lang, "admin.stDeviceLimited")
	default:
		return esc(status)
	}
}

// userButtonLabel is the short label on a user's list button.
func userButtonLabel(u model.User) string {
	return fmt.Sprintf("%s #%d %s · %s", statusEmoji(u.Status), u.ID, u.Name, formatBytes(u.UsedUp+u.UsedDown))
}

// userCard is the per-user detail view (no links — those are a separate button so
// the card stays compact and navigable).
func userCard(u model.User, loc *time.Location, lang i18n.Lang) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>#%d %s</b>\n", u.ID, esc(u.Name))
	fmt.Fprintf(&b, "%s\n", i18n.T(lang, "admin.cardStatus", statusLabel(u.Status, lang)))
	used := formatBytes(u.UsedUp + u.UsedDown)
	if u.DataLimit > 0 {
		fmt.Fprintf(&b, "%s\n", i18n.T(lang, "admin.cardTraffic", used, formatBytes(u.DataLimit)))
	} else {
		fmt.Fprintf(&b, "%s\n", i18n.T(lang, "admin.cardTrafficUnlimited", used))
	}
	if u.ExpireAt > 0 {
		fmt.Fprintf(&b, "%s\n", i18n.T(lang, "admin.cardExpires", time.Unix(u.ExpireAt, 0).In(loc).Format("2006-01-02 15:04")))
	} else {
		fmt.Fprintf(&b, "%s\n", i18n.T(lang, "admin.cardNoExpiry"))
	}
	if u.LastSeen > 0 {
		fmt.Fprintf(&b, "%s", i18n.T(lang, "admin.cardLastSeen", time.Unix(u.LastSeen, 0).In(loc).Format("2006-01-02 15:04")))
	} else {
		b.WriteString(i18n.T(lang, "admin.cardNeverSeen"))
	}
	return b.String()
}

// planButtonLabel is the inline-button text for a tariff plan. A free plan is just
// its name: the price suffix said nothing the plan list didn't already imply, and
// the end-user purchase list never shows free plans at all — only the admin bot's
// "assign a plan" list reaches them.
func planButtonLabel(p model.TariffPlan, lang i18n.Lang) string {
	if p.PriceRub > 0 && p.PeriodDays > 0 {
		return i18n.T(lang, "admin.planPrice", p.Name, p.PriceRub, p.PeriodDays)
	}
	if p.PriceRub > 0 {
		return fmt.Sprintf("%s · %d ₽", p.Name, p.PriceRub)
	}
	return p.Name
}

// userCardWithPlan extends userCard with the active billing plan (if any).
func userCardWithPlan(u model.User, loc *time.Location, planName string, billingOn bool, lang i18n.Lang) string {
	card := userCard(u, loc, lang)
	switch {
	case planName != "":
		card += "\n" + i18n.T(lang, "admin.planIs", esc(planName))
	case billingOn && u.PlanID == 0:
		card += "\n" + i18n.T(lang, "admin.planManual")
	}
	return card
}

// subCaption is the caption shown with the subscription QR: the user's
// subscription URL (the QR encodes the same URL).
func subCaption(u model.User, set *model.Settings, lang i18n.Lang) string {
	return fmt.Sprintf(i18n.T(lang, "admin.subCard"),
		u.ID, esc(u.Name), esc(sub.URL(set, u.SubToken)))
}

// subQR renders the user's subscription URL as a PNG QR code.
func subQR(u model.User, set *model.Settings) ([]byte, error) {
	return qrcode.Encode(sub.URL(set, u.SubToken), qrcode.Medium, 512)
}

// sleep waits d or returns false early if ctx is cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
