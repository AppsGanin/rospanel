package telegram

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The welcome names what signing up accepts — only the documents that have text,
// linked — and says nothing without any; the menu's button follows the same rule.
func TestLegalAcceptLine(t *testing.T) {
	t.Parallel()
	st, err := store.Open(filepath.Join(t.TempDir(), "legal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &UserService{store: st}
	if err := st.ExecForTest(`UPDATE settings SET host = 'vpn.example.com', legal_path = 'lp' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings()
	if line := s.legalAcceptLine(set, i18n.RU); line != "" || s.legalMenuRow(set, i18n.RU) != nil {
		t.Fatalf("no documents, yet %q", line)
	}
	if err := st.SetLegalDoc("privacy", "Policy", 1); err != nil {
		t.Fatal(err)
	}
	line := s.legalAcceptLine(set, i18n.RU)
	if line != `Регистрируясь, вы принимаете <a href="https://vpn.example.com/sub/lp/privacy">Политику конфиденциальности</a>.` {
		t.Errorf("one document: %q", line)
	}
	if err := st.SetLegalDoc("terms", "Terms", 1); err != nil {
		t.Fatal(err)
	}
	if line := s.legalAcceptLine(set, i18n.EN); !strings.Contains(line, "/terms\">User agreement</a> and the <a") {
		t.Errorf("both: %q", line)
	}
	if row := s.legalMenuRow(set, i18n.RU); len(row) != 1 || row[0].CallbackData != "vu:legal" {
		t.Errorf("menu row: %+v", row)
	}
}
