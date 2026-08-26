package core

import (
	"testing"

	"github.com/Shu1t3/rospanel-shu1t3/internal/abuse"
	"github.com/Shu1t3/rospanel-shu1t3/internal/i18n"
)

// Some keys are not written at the call site: the abuse alert keys off the category
// constant, and a registration decision passes the key down as a variable. The
// repo-wide call-site check cannot see either, and T falls back to the key — so a
// gap here reaches the operator as "abuse.badip" in a Telegram message. These are
// the two places that build a key, so they are checked by hand.
func TestRuntimeAssembledKeysResolve(t *testing.T) {
	cats := []abuse.Category{
		abuse.CatCustom, abuse.CatBadIP, abuse.CatMalware, abuse.CatPiracy, abuse.CatGambling,
	}
	var keys []string
	for _, c := range cats {
		keys = append(keys, c.TitleKey())
	}
	// The three outcomes notifyRegistrationDecision is called with.
	keys = append(keys, "notify.regAlreadyLinked", "notify.regApproved", "notify.regRejected")

	for _, lang := range []i18n.Lang{i18n.RU, i18n.EN} {
		for _, k := range keys {
			if got := i18n.T(lang, k); got == k {
				t.Errorf("%s: %q has no catalog entry (rendered as itself)", lang, k)
			}
		}
	}
}
