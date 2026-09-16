package model

import "slices"

// Clone returns a copy of the inbound that shares no memory with it: its options carry
// raw JSON and lists, which a plain copy of the struct would still share. What one
// subscription does with the inbounds it is handed must not reach the next one's.
//
// A reference field added to Inbound or InboundOpts has to be copied here:
// TestInboundCloneSharesNothing fills every field and fails on any the copy still shares.
func (in Inbound) Clone() Inbound {
	in.Opts.XHTTPExtra = slices.Clone(in.Opts.XHTTPExtra)
	in.Opts.HeaderHosts = slices.Clone(in.Opts.HeaderHosts)
	in.Opts.HeaderPaths = slices.Clone(in.Opts.HeaderPaths)
	in.Opts.Sockopt = slices.Clone(in.Opts.Sockopt)
	in.Opts.TLSExtra = slices.Clone(in.Opts.TLSExtra)
	return in
}
