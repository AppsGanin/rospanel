package xray

// ClientEmails lists the email tags of every user a generated config lets in, across
// all inbounds, in inbound order and with repeats (a user on three lanes is listed
// three times). It is what a server was given: a node reporting traffic, connections
// or destinations for anyone else is reporting users it never had.
//
// ok is false when an inbound of a protocol that carries users holds settings of a
// type this does not know — the generator started building one differently — so the
// caller can tell "no users" from "could not read them".
func (c *Config) ClientEmails() (emails []string, ok bool) {
	for _, in := range c.Inbounds {
		if liveUserKey[in.Protocol] == "" {
			continue // api, socks and http inbounds carry no users of the panel
		}
		switch s := in.Settings.(type) {
		case VLESSInboundSettings:
			for _, u := range s.Clients {
				emails = append(emails, u.Email)
			}
		case TrojanInboundSettings:
			for _, u := range s.Clients {
				emails = append(emails, u.Email)
			}
		case HysteriaInboundSettings:
			for _, u := range s.Users {
				emails = append(emails, u.Email)
			}
		case ShadowsocksInboundSettings:
			for _, u := range s.Users {
				emails = append(emails, u.Email)
			}
		case WireGuardInboundSettings:
			for _, u := range s.Peers {
				emails = append(emails, u.Email)
			}
		default:
			return nil, false
		}
	}
	return emails, true
}
