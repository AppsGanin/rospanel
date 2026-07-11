// Package actor carries WHO is performing an action across layer boundaries.
//
// The audit log needs to attribute every mutation, but core.Manager sits below the
// HTTP and bot layers and can't reach a session cookie or a Telegram update. So the
// actor rides the request context: each entry point (panel session, API key,
// Telegram bot, subscription page) stamps it once, and the Manager reads it back
// when it writes its audit row.
//
// This lives in its own leaf package rather than in core so the bots can stamp an
// actor without importing core — the telegram package deliberately depends on core
// only through its own narrow Panel interface.
package actor

import (
	"context"

	"github.com/AppsGanin/rospanel/internal/model"
)

// Actor is who is performing an action: a kind (model.Actor*) and a display name.
type Actor struct {
	Kind string
	Name string
}

type ctxKey struct{}

// System is the fallback for anything the panel does on its own initiative — the
// background poller, a provider webhook. A context with no actor means exactly this.
var System = Actor{Kind: model.ActorSystem}

// Admin / APIKey / Telegram / UserSelf name the four external entry points, so
// callers don't hand-roll the kind strings.
func Admin(username string) Actor { return Actor{Kind: model.ActorAdmin, Name: username} }
func APIKey(name string) Actor    { return Actor{Kind: model.ActorAPIKey, Name: name} }
func Telegram(name string) Actor  { return Actor{Kind: model.ActorTelegram, Name: name} }
func UserSelf(name string) Actor  { return Actor{Kind: model.ActorUser, Name: name} }

// With stamps the actor onto ctx for the mutating calls made under it.
func With(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

// From returns the actor stamped on ctx, or System when none is.
func From(ctx context.Context) Actor {
	if a, ok := ctx.Value(ctxKey{}).(Actor); ok && a.Kind != "" {
		return a
	}
	return System
}
