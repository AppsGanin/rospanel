//go:build !linux

package awg

// stubDevice stands in where there is no TUN support in this build. Configs are
// still generated and validated; only the tunnel itself is absent.
type stubDevice struct{}

// New returns the tunnel driver for this platform.
func New() Device { return stubDevice{} }

func (stubDevice) Apply(cfg Config) error {
	if _, err := cfg.UAPI(); err != nil {
		return err
	}
	return ErrUnsupported
}
func (stubDevice) Stats() (map[string]PeerStat, error) { return nil, nil }
func (stubDevice) Running() bool                       { return false }
func (stubDevice) LastError() string                   { return ErrUnsupported.Error() }
func (stubDevice) Close()                              {}
