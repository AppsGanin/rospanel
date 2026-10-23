package core

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A server's WARP and Opera handed to users as connections (model.ExitConfig). They
// are the server's own, like its routing: the master's in settings, a node's on its
// row; the node's Xray config is generated here, so either way the change is a reload.

// SetExitConfigs replaces server serverID's exit configs, as read from the editor: a
// kind with no lane is not handed out.
func (m *Manager) SetExitConfigs(serverID int64, list []model.ExitConfig) ([]model.ExitConfig, error) {
	clean := []model.ExitConfig{}
	for _, c := range list {
		if _, ok := model.ExitRoute(c.Kind); !ok {
			return nil, invalidCode("err.exitKind", "неизвестный выход")
		}
		if slices.ContainsFunc(clean, func(x model.ExitConfig) bool { return x.Kind == c.Kind }) {
			continue
		}
		c.Name = strings.TrimSpace(c.Name)
		if utf8.RuneCountInString(c.Name) > model.ExitNameMax || strings.ContainsAny(c.Name, "\r\n\t") {
			return nil, invalidCode("err.extServerName", "название — одна строка, не длиннее {{max}} символов", map[string]any{"max": model.ExitNameMax})
		}
		var lanes []string
		for _, l := range c.Lanes {
			if (l == model.LaneVLESS || l == model.LaneReality) && !slices.Contains(lanes, l) {
				lanes = append(lanes, l)
			}
		}
		slices.Sort(lanes)
		c.Lanes = lanes
		if len(lanes) == 0 && c.Name == "" {
			continue
		}
		if c.Lanes == nil {
			c.Lanes = []string{}
		}
		clean = append(clean, c)
	}
	if serverID == model.LocalNodeID {
		if err := m.store.SetExitConfigs(clean); err != nil {
			return nil, err
		}
	} else {
		n, err := m.store.GetNode(serverID)
		if err != nil {
			return nil, err
		}
		if n == nil {
			return nil, invalidCode("err.nodeNotFound", "нода не найдена")
		}
		if err := m.store.SetNodeExitConfigs(serverID, clean); err != nil {
			return nil, err
		}
	}
	logInfo("exits: set", "server", serverID, "configs", len(clean))
	m.TriggerReconcile()
	m.notifyNodes()
	return clean, nil
}
