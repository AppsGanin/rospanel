package core

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/awg"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Extra tunnel keys, one per device (migration 0147), for AmneziaWG and the WireGuard
// inbounds (TURN-VK included) alike — the lanes that share the user's main key.
// WireGuard knows a peer by its key and sends to wherever that key last spoke from, so
// one key on two devices is one device at a time. Each extra key is a peer of its own
// on every server, under the user's email: its traffic, sightings and limits are the
// user's.
//
// A key is made by hand (the user card, the subscription page) or for a subscription
// client that sends x-hwid and reads AmneziaWG, on its first fetch. How many a user
// may hold, the main key included, is the HWID device cap.

// UserAWGKeys returns a user's extra keys and how many keys they may hold in all.
func (m *Manager) UserAWGKeys(userID int64) ([]model.AWGDevice, int, error) {
	u, err := m.store.GetUser(userID)
	if err != nil {
		return nil, 0, err
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, 0, err
	}
	list, err := m.store.ListAWGDevices(userID)
	if err != nil {
		return nil, 0, err
	}
	return list, set.AWGKeyCap(*u), nil
}

// AWGInUse reports whether any server has a lane the device keys serve switched on —
// AmneziaWG, or a WireGuard inbound (TURN-VK included): the card shows them only then.
func (m *Manager) AWGInUse() bool {
	if set, err := m.store.GetSettings(); err == nil && set.AWGEnabled {
		return true
	}
	if nodes, err := m.store.ListNodes(); err == nil {
		for i := range nodes {
			if nodes[i].Enabled && derefBool(nodes[i].AWGEnabled) {
				return true
			}
		}
	}
	all, err := m.store.AllInbounds()
	if err != nil {
		return false
	}
	for _, list := range all {
		for _, in := range list {
			if in.Enabled && model.IsWireGuard(in.Protocol) {
				return true
			}
		}
	}
	return false
}

// cleanAWGDeviceName trims a device's name to one short line.
func cleanAWGDeviceName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	for utf8.RuneCountInString(name) > model.AWGDeviceNameMax {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

// AddUserAWGKey gives a user a new device key. hwid is the subscription client it is
// for, "" for one added by hand; a hwid that already has a key gets that key back.
// created says whether a key was made.
func (m *Manager) AddUserAWGKey(ctx context.Context, userID int64, name, hwid string) (dev *model.AWGDevice, created bool, err error) {
	u, err := m.store.GetUser(userID)
	if err != nil {
		return nil, false, err
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, false, err
	}
	// The main key first: it is key number one of the cap, and its slot is the one
	// a fresh user's configs already carry.
	if err := m.claimAWG([]*model.User{u}); err != nil {
		return nil, false, err
	}
	priv, _, err := awg.GenerateKey()
	if err != nil {
		return nil, false, err
	}
	d := model.AWGDevice{UserID: userID, Name: cleanAWGDeviceName(name), HWID: hwid, PrivateKey: priv, CreatedAt: time.Now().Unix()}
	got, err := m.store.CreateAWGDevice(d, set.AWGKeyCap(*u), awg.FirstSlot, awg.LastSlot)
	switch {
	case errors.Is(err, store.ErrAWGKeysFull):
		return nil, false, invalidCode("err.awgKeysFull", "достигнут лимит устройств: {{value}}",
			map[string]any{"value": set.AWGKeyCap(*u)})
	case errors.Is(err, store.ErrAWGSubnetFull):
		return nil, false, invalidCode("err.awgSubnetFull", "в подсети AmneziaWG не осталось адресов")
	case err != nil:
		return nil, false, err
	}
	if got.PrivateKey != priv { // the hwid had one already
		return got, false, nil
	}
	m.audit(ctx, userID, model.EventAWGKeyAdded, map[string]any{"name": got.Name, "hwid": hwid})
	m.awgKeysChanged()
	return got, true, nil
}

// RenameUserAWGKey renames one of a user's device keys.
func (m *Manager) RenameUserAWGKey(userID, id int64, name string) error {
	ok, err := m.store.RenameAWGDevice(userID, id, cleanAWGDeviceName(name))
	if err != nil {
		return err
	}
	if !ok {
		return invalidCode("err.awgKeyNotFound", "ключ не найден")
	}
	return nil
}

// DeleteUserAWGKey removes one of a user's device keys: the device it was on loses
// the tunnel on every server with the next sync.
func (m *Manager) DeleteUserAWGKey(ctx context.Context, userID, id int64) error {
	d, err := m.store.GetAWGDevice(userID, id)
	if err != nil {
		return err
	}
	if d == nil {
		return invalidCode("err.awgKeyNotFound", "ключ не найден")
	}
	if _, err := m.store.DeleteAWGDevice(userID, id); err != nil {
		return err
	}
	m.audit(ctx, userID, model.EventAWGKeyRemoved, map[string]any{"name": d.Name, "hwid": d.HWID})
	m.awgKeysChanged()
	return nil
}

// AWGKeyByID returns a user's device key as a tunnel identity; nil when they have
// no such key, or it is past their device cap (model.ActiveAWGDevices) and so not a
// peer anywhere: a config for it would not connect.
func (m *Manager) AWGKeyByID(userID, id int64) (*model.AWGKey, error) {
	list, capKeys, err := m.UserAWGKeys(userID)
	if err != nil {
		return nil, err
	}
	for i, d := range list[:model.ActiveAWGDevices(len(list), capKeys)] {
		if d.ID == id {
			return &model.AWGKey{ID: d.ID, Key: list[i].PrivateKey, Slot: d.Slot}, nil
		}
	}
	return nil, nil
}

// AWGKeyForHWID returns the key a subscription client sending x-hwid writes its
// AmneziaWG configs with: its own, made on its first fetch while the user has room.
// nil means the main key: no hwid, no room, or nothing could be made.
func (m *Manager) AWGKeyForHWID(ctx context.Context, userID int64, hwid, name string) *model.AWGKey {
	if hwid == "" {
		return nil
	}
	d, err := m.store.AWGDeviceByHWID(userID, hwid)
	if err == nil && d == nil {
		d, _, err = m.AddUserAWGKey(ctx, userID, name, hwid)
	}
	if err != nil || d == nil {
		// Full is the common case and not worth a log line: the client gets the main key.
		var ve *ValidationError
		if err != nil && !errors.As(err, &ve) {
			logErr("awg: device key for a client", "user", userID, "err", err)
		}
		return nil
	}
	// A key past a cap lowered since is no peer: the main key it is, then.
	key, err := m.AWGKeyByID(userID, d.ID)
	if err != nil {
		return nil
	}
	return key
}

// rekeyAWGDevices gives every device key of a user a new private key, with the
// user's own key (ResetUserUUID).
func (m *Manager) rekeyAWGDevices(userID int64) error {
	return m.store.RekeyAWGDevices(userID, func() (string, error) {
		priv, _, err := awg.GenerateKey()
		return priv, err
	})
}

// awgKeysChanged pushes a change of device keys to the master's tunnel and the nodes.
func (m *Manager) awgKeysChanged() {
	m.awgKeysDirty.Store(true)
	m.dropNodeInputs()
	m.TriggerUserSync()
}
