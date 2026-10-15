package link

import (
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/mtproto"
)

// MTProtoOn reports whether a server hands out its Telegram proxy: the lane is on and
// has a port.
func MTProtoOn(set *model.Settings) bool { return set.MTProtoEnabled && set.MTProtoPort > 0 }

// MTProto is a user's Telegram proxy link (tg://proxy), which adds the proxy to Telegram
// in one tap.
func MTProto(u model.User, set *model.Settings) string {
	return mtproto.Link(set.Host, set.MTProtoPort, set.MTProtoDomainOr(), u.UUID)
}

// MTProtoWeb is MTProto as an https://t.me/proxy address, for where tg:// does not open.
func MTProtoWeb(u model.User, set *model.Settings) string {
	return mtproto.WebLink(set.Host, set.MTProtoPort, set.MTProtoDomainOr(), u.UUID)
}
