package happ

import (
	"context"
	"fmt"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/netguard"
)

const (
	maxSubBodyBytes = 2 * 1024 * 1024 // 2 MB
	fetchTimeout    = 30 * time.Second
)

// FetchResult is the result of a single subscription fetch-and-parse.
type FetchResult struct {
	Nodes []Node
	Raw   []byte // raw body for diagnostics
}

// Fetch downloads a subscription URL, decodes its body, and parses all proxy
// URIs into Node values. It uses the existing SSRF-safe netguard HTTP client
// (https-only, private IPs blocked, redirect-checked, size-limited).
func Fetch(ctx context.Context, rawURL string, subscriptionID int64) (*FetchResult, error) {
	if err := netguard.ValidateFetchURL(rawURL); err != nil {
		return nil, fmt.Errorf("subscription URL invalid: %w", err)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, fetchTimeout)
		defer cancel()
	}

	body, err := netguard.Get(ctx, rawURL, maxSubBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}

	lines := Decode(body)
	nodes := ParseURIs(lines, subscriptionID)

	return &FetchResult{
		Nodes: nodes,
		Raw:   body,
	}, nil
}
