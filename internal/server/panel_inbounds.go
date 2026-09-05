package server

import (
	"net/http"
	"strings"

	"github.com/AppsGanin/rospanel/internal/auth"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Custom-inbound endpoints. Every route is keyed by SERVER id (0 = the master, a
// node id otherwise), because an inbound belongs to exactly one machine — its port,
// its REALITY identity and its hop range are all facts about that box.

// serverInbounds lists one server's custom inbounds.
func (rt *Router) serverInbounds(w http.ResponseWriter, _ *http.Request, serverID int64) {
	list, err := rt.mgr.Inbounds(serverID)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// inboundReq is the editable shape of a custom inbound. It deliberately mirrors
// model.Inbound rather than accepting it directly, so no request can set the fields
// the panel owns — the row id, the server, or the REALITY private key.
type inboundReq struct {
	Enabled  bool   `json:"enabled"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`

	Transport string `json:"transport"`
	Security  string `json:"security"`
	SNI       string `json:"sni"`
	FP        string `json:"fp"`
	Path      string `json:"path"`
	Host      string `json:"host"`
	Mode      string `json:"mode"`

	ServiceName string `json:"service_name"`

	RealityDest       string `json:"reality_dest"`
	RealityAntiReplay bool   `json:"reality_anti_replay"`

	HopStart    int    `json:"hop_start"`
	HopEnd      int    `json:"hop_end"`
	HopInterval string `json:"hop_interval"`
	// Obfs is the Salamander key the editor round-trips; RegenObfs asks for a fresh
	// one, which is the only way it ever changes from the panel — the field is shown
	// read-only, like the REALITY material beside it.
	Obfs      string `json:"obfs"`
	RegenObfs bool   `json:"regen_obfs"`

	// Shadowsocks-2022 method. The server key is generated (prepareInbound), so it is
	// deliberately not a request field.
	Method string `json:"method"`

	// Advanced. Each transport knob is its own typed field; the three JSON-blob
	// sections (XHTTP extra, sockopt, extra TLS) arrive as structured forms that the
	// server assembles into the blob Xray reads. Every form carries a Raw escape hatch
	// (`*_raw`) for keys the panel does not surface as a field.
	HeaderType  string   `json:"header_type"`
	HeaderHosts []string `json:"header_hosts"`
	HeaderPaths []string `json:"header_paths"`
	Authority   string   `json:"authority"`
	MultiMode   bool     `json:"multi_mode"`

	XHTTPExtra model.XHTTPExtraForm `json:"xhttp_extra"`
	Sockopt    model.SockoptForm    `json:"sockopt"`
	TLSExtra   model.TLSExtraForm   `json:"tls_extra"`
}

// toModel converts the request into a domain inbound. serverID and id come from the
// route, never the body. The three advanced forms are assembled into the JSON blobs
// InboundOpts stores; validation (whitelist + xray -test) then runs on those blobs.
func (r inboundReq) toModel(serverID, id int64) (model.Inbound, error) {
	in := model.Inbound{
		ID:       id,
		ServerID: serverID,
		Enabled:  r.Enabled,
		Name:     r.Name,
		Protocol: r.Protocol,
		Port:     r.Port,
		Opts: model.InboundOpts{
			Transport:   r.Transport,
			Security:    r.Security,
			SNI:         r.SNI,
			FP:          r.FP,
			Path:        r.Path,
			Host:        r.Host,
			Mode:        r.Mode,
			ServiceName: r.ServiceName,
			RealityDest: r.RealityDest,
			HopStart:    r.HopStart,
			HopEnd:      r.HopEnd,
			HopInterval: r.HopInterval,
			Obfs:        r.Obfs,
			HeaderType:  r.HeaderType,
			HeaderHosts: r.HeaderHosts,
			HeaderPaths: r.HeaderPaths,
			Authority:   r.Authority,
			MultiMode:   r.MultiMode,
			Method:      r.Method,
		},
	}
	if r.RealityAntiReplay {
		in.Opts.RealityMaxTimeDiff = realityAntiReplayWindowMs
	}
	if r.RegenObfs {
		// Minted here rather than in the browser so a key can only ever come from the
		// panel's own generator — see auth.RandomObfsKey.
		key, err := auth.RandomObfsKey()
		if err != nil {
			return in, err
		}
		in.Opts.Obfs = key
	}
	var err error
	if in.Opts.XHTTPExtra, err = model.AssembleXHTTPExtra(r.XHTTPExtra); err != nil {
		return in, rawFieldErr("XHTTP extra", err)
	}
	if in.Opts.Sockopt, err = model.AssembleSockopt(r.Sockopt); err != nil {
		return in, rawFieldErr("sockopt", err)
	}
	if in.Opts.TLSExtra, err = model.AssembleTLSExtra(r.TLSExtra); err != nil {
		return in, rawFieldErr("TLS extra", err)
	}
	return in, nil
}

// rawFieldErr reports a raw-JSON box that would not parse. The box's name is a
// technical one ("sockopt"), identical in both languages, so it rides as an
// argument; the sentence around it is a code the panel words.
func rawFieldErr(field string, err error) error {
	return model.FieldErr("err.rawFieldBadJSON", "{{field}}: не разбирается как JSON ({{detail}})",
		map[string]any{"field": field, "detail": jsonErr(err)})
}

// jsonErr softens a raw json error into something an operator reads as "the JSON
// you typed in the raw box doesn't parse". Its own text is deliberately not a
// dictionary key: what it returns on the else branch is the json package's message,
// which is English whatever we do.
func jsonErr(err error) string {
	if strings.Contains(err.Error(), "json") || strings.Contains(err.Error(), "invalid") {
		return "not valid JSON"
	}
	return err.Error()
}

// realityAntiReplayWindowMs mirrors core's REALITY maxTimeDiff — a ±60s window,
// generous enough not to reject phones with a skewed clock.
const realityAntiReplayWindowMs = 60000

// createServerInbound adds a custom inbound to one server.
func (rt *Router) createServerInbound(w http.ResponseWriter, r *http.Request, serverID int64) {
	var req inboundReq
	if !decodeJSON(w, r, &req) {
		return
	}
	in, err := req.toModel(serverID, 0)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	v, err := rt.mgr.CreateInbound(r.Context(), in)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// updateInbound edits one custom inbound. The server it belongs to is taken from
// the stored row, so this route needs only the inbound id.
func (rt *Router) updateInbound(w http.ResponseWriter, r *http.Request, id int64) {
	var req inboundReq
	if !decodeJSON(w, r, &req) {
		return
	}
	in, err := req.toModel(0, id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	v, err := rt.mgr.UpdateInbound(r.Context(), in)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// deleteInbound removes one custom inbound.
func (rt *Router) deleteInbound(w http.ResponseWriter, _ *http.Request, id int64) {
	if err := rt.mgr.DeleteInbound(id); err != nil {
		writeManagerErr(w, err)
		return
	}
	writeOK(w)
}

// regenInboundReality mints fresh REALITY material for one inbound. Its own route
// rather than part of an edit: every client using this lane has to re-import.
func (rt *Router) regenInboundReality(w http.ResponseWriter, _ *http.Request, id int64) {
	v, err := rt.mgr.RegenInboundReality(id)
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// inboundCombo is one protocol × transport the panel accepts, with the security
// modes it allows and the subscription formats that cannot carry it.
type inboundCombo struct {
	Protocol   string   `json:"protocol"`
	Transport  string   `json:"transport"`
	Securities []string `json:"securities"`
	// Unsupported names the subscription formats that cannot carry this
	// combination, so the editor can warn before the operator commits to it.
	Unsupported []string `json:"unsupported"`
}

// inboundCatalogView is the static "what can be combined with what" table, shared by
// the panel editor and the external API so neither can build an inbound the
// server-side validator will reject (see model.InboundSecurities).
type inboundCatalogView struct {
	Protocols    []string       `json:"protocols"`
	Combos       []inboundCombo `json:"combos"`
	Fingerprints []string       `json:"fingerprints"`
	XHTTPModes   []string       `json:"xhttp_modes"`
	Max          int            `json:"max"` // inbounds per server
	// Enums are the advanced-field dropdowns, straight from Xray's parser (see
	// model), so the editor's options and the server's validation can't disagree.
	Enums map[string][]string `json:"enums"`
}

func makeInboundCatalog() inboundCatalogView {
	combos := []inboundCombo{}
	for _, p := range model.InboundProtocols {
		for _, tr := range model.InboundTransports(p) {
			var unsupported []string
			if !model.SupportsClash(p, tr) {
				unsupported = append(unsupported, "Clash / Mihomo")
			}
			if !model.SupportsSingBox(p, tr) {
				unsupported = append(unsupported, "sing-box / Hiddify")
			}
			combos = append(combos, inboundCombo{
				Protocol:    p,
				Transport:   tr,
				Securities:  model.InboundSecurities(p, tr),
				Unsupported: unsupported,
			})
		}
	}
	return inboundCatalogView{
		Protocols:    model.InboundProtocols,
		Combos:       combos,
		Fingerprints: model.Fingerprints,
		XHTTPModes:   []string{model.XHTTPAuto, model.XHTTPPacketUp, model.XHTTPStreamUp, model.XHTTPStreamOne},
		Max:          model.MaxInboundsPerServer,
		Enums: map[string][]string{
			"placements":            model.XHTTPPlacements,
			"uplink_methods":        model.XHTTPUplinkMethods,
			"tproxy":                model.SockoptTProxy,
			"domain_strategy":       model.SockoptDomainStrats,
			"address_port_strategy": model.SockoptAddrPortStrats,
			"tls_versions":          model.TLSVersions,
			"ss_methods":            model.SSMethods,
		},
	}
}

func (rt *Router) inboundCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, makeInboundCatalog())
}
