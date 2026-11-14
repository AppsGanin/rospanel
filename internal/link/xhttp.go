package link

import "github.com/AppsGanin/rospanel/internal/model"

// XHTTPInbound is the VLESS-XHTTP-TLS lane as the custom inbound it looks like to a
// client — VLESS over XHTTP and TLS on the TCP-TLS lane's port, on the lane's path, with
// the server's own name and certificate — so its link and its Clash entry are built the
// way a custom XHTTP inbound's are. Its name is the lane's label, with any variables
// still in it for the custom-inbound labelling to expand.
func XHTTPInbound(set *model.Settings) model.Inbound {
	name := model.ProtoXHTTP
	if set.XHTTPName != "" {
		name = set.XHTTPName
	}
	return model.Inbound{
		Name:     name,
		Protocol: model.InbVLESS,
		Port:     set.VLESSPort,
		Enabled:  true,
		Opts: model.InboundOpts{
			Transport: model.TrXHTTP,
			Security:  model.SecTLS,
			Path:      set.XHTTPPath,
			FP:        set.VLESSFP(),
		},
	}
}

// XHTTP is a user's vless:// link for the VLESS-XHTTP-TLS lane:
//
//	vless://<uuid>@<host>:<vless port>?encryption=none&security=tls&sni=<sni>&fp=…
//	      &alpn=h2,http/1.1&type=xhttp&path=<path>&host=<sni>#<label>
//
// with the certificate pinned (pcs) when no CA vouches for it, as on VLESS-TCP-TLS.
func XHTTP(u model.User, set *model.Settings) string {
	return Custom(u, XHTTPInbound(set), set)
}
