package model

// AWGParams are a server's AmneziaWG obfuscation parameters as the store keeps
// them — the same fields as awg.Params, kept here so the model does not pull the
// tunnel engine into every package that reads a settings row. See internal/awg
// for what each one does.
type AWGParams struct {
	Jc   int    `json:"jc"`
	Jmin int    `json:"jmin"`
	Jmax int    `json:"jmax"`
	S1   int    `json:"s1"`
	S2   int    `json:"s2"`
	H1   uint32 `json:"h1"`
	H2   uint32 `json:"h2"`
	H3   uint32 `json:"h3"`
	H4   uint32 `json:"h4"`
}

// IsZero reports a parameter block that was never generated.
func (p AWGParams) IsZero() bool { return p == AWGParams{} }
