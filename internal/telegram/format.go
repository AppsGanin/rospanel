package telegram

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/sub"
)

// menuHeader is the main-menu caption.
const menuHeader = "<b>Меню управления</b>\nВыберите действие:"

// accessDenied is shown to a chat that isn't linked.
const accessDenied = "🚫 Доступ запрещён.\n\nЭто админ-бот панели. Откройте раздел <b>Telegram</b> в настройках, сгенерируйте код и отправьте <code>/start КОД</code>.\n\nДля VPN-подписки используйте отдельного пользовательского бота."

// mainMenuRows is the top-level inline keyboard.
func mainMenuRows() [][]InlineButton {
	return [][]InlineButton{
		{{Text: "👥 Пользователи", CallbackData: "users:0"}},
		{{Text: "📦 Бэкап", CallbackData: "backup"}},
	}
}

// backToMenu is the single "back to menu" row reused by leaf views.
func backToMenu() [][]InlineButton {
	return [][]InlineButton{{{Text: "⬅️ Меню", CallbackData: "menu"}}}
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

// parseSize parses a data-limit value: a plain byte count, or a number with a
// K/M/G/T suffix (base 1024, optional trailing "B"/"iB"). "0" means unlimited.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	s = strings.TrimSuffix(s, "IB")
	s = strings.TrimSuffix(s, "B")
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K':
		mult, s = 1<<10, s[:len(s)-1]
	case 'M':
		mult, s = 1<<20, s[:len(s)-1]
	case 'G':
		mult, s = 1<<30, s[:len(s)-1]
	case 'T':
		mult, s = 1<<40, s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid size")
	}
	return int64(f * float64(mult)), nil
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
func statusLabel(status string) string {
	switch status {
	case "active":
		return "✅ активен"
	case "disabled":
		return "⛔ выключен"
	case "expired":
		return "⌛ истёк"
	case "limited":
		return "📵 лимит"
	case "device_limited":
		return "📱 лишние устройства"
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
func userCard(u model.User, loc *time.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>#%d %s</b>\n", u.ID, esc(u.Name))
	fmt.Fprintf(&b, "Статус: %s\n", statusLabel(u.Status))
	used := formatBytes(u.UsedUp + u.UsedDown)
	if u.DataLimit > 0 {
		fmt.Fprintf(&b, "Трафик: %s / %s\n", used, formatBytes(u.DataLimit))
	} else {
		fmt.Fprintf(&b, "Трафик: %s (без лимита)\n", used)
	}
	if u.ExpireAt > 0 {
		fmt.Fprintf(&b, "Истекает: %s\n", time.Unix(u.ExpireAt, 0).In(loc).Format("2006-01-02 15:04"))
	} else {
		b.WriteString("Истекает: бессрочно\n")
	}
	if u.LastSeen > 0 {
		fmt.Fprintf(&b, "Был онлайн: %s", time.Unix(u.LastSeen, 0).In(loc).Format("2006-01-02 15:04"))
	} else {
		b.WriteString("Был онлайн: никогда")
	}
	return b.String()
}

// planButtonLabel is the inline-button text for a tariff plan.
func planButtonLabel(p model.TariffPlan) string {
	if p.IsFree() {
		return p.Name + " · бесплатно"
	}
	if p.PriceRub > 0 && p.PeriodDays > 0 {
		return fmt.Sprintf("%s · %d ₽ / %d дн.", p.Name, p.PriceRub, p.PeriodDays)
	}
	if p.PriceRub > 0 {
		return fmt.Sprintf("%s · %d ₽", p.Name, p.PriceRub)
	}
	return p.Name
}

// userCardWithPlan extends userCard with the active billing plan (if any).
func userCardWithPlan(u model.User, loc *time.Location, planName string, billingOn bool) string {
	card := userCard(u, loc)
	switch {
	case planName != "":
		card += "\nТариф: " + esc(planName)
	case billingOn && u.PlanID == 0:
		card += "\nТариф: вручную"
	}
	return card
}

// subCaption is the caption shown with the subscription QR: the user's
// subscription URL (the QR encodes the same URL).
func subCaption(u model.User, set *model.Settings) string {
	return fmt.Sprintf("<b>Подписка — #%d %s</b>\n\n<code>%s</code>",
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
