package server

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The Shadowsocks method must survive the request→model mapping. It very nearly
// didn't: inboundReq had no method field, so the strict external decoder rejected
// the request outright ("unknown field method") and the lenient panel decoder
// dropped it silently — an operator's choice of cipher vanishing on the way in,
// which is the worse of the two. The whole feature is unreachable without this, and
// no model-level test sees it because they build the Inbound directly.
func TestInboundReqCarriesShadowsocksMethod(t *testing.T) {
	req := inboundReq{
		Name: "ss", Protocol: model.InbShadowsocks, Port: 24900,
		Method: model.SS2022AES256,
	}
	in, err := req.toModel(model.LocalNodeID, 0)
	if err != nil {
		t.Fatalf("toModel: %v", err)
	}
	if in.Opts.Method != model.SS2022AES256 {
		t.Errorf("method lost in mapping: got %q, want %q", in.Opts.Method, model.SS2022AES256)
	}
	// And it normalizes + validates as a real inbound once the panel fills the key.
	in.Normalize()
	if in.Opts.Method != model.SS2022AES256 {
		t.Errorf("normalize dropped the chosen method: %q", in.Opts.Method)
	}
}
