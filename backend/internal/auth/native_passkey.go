package auth

import (
	"context"
	"database/sql"
	"net/url"
)

// Native identities are logical authentication identities, not transport URLs.
// A challenge still binds the actual approved transport origin independently.
type NativeBinding struct {
	ID     string `json:"id"`
	RPID   string `json:"rp_id"`
	Origin string `json:"origin"`
}
type ClientConfig struct {
	APIVersion     string          `json:"api_version"`
	BackendID      string          `json:"backend_id"`
	NativePasskey  NativeBinding   `json:"native_passkey"`
	LegacyBindings []NativeBinding `json:"legacy_bindings"`
}
type NativeProofBinding struct {
	BackendID       string        `json:"backend_id"`
	TransportOrigin string        `json:"transport_origin"`
	Binding         NativeBinding `json:"binding"`
}
type nativeContextKey struct{}

func ensureNativeIdentity(db *sql.DB, origin string) error {
	id, e := RandomToken()
	if e != nil {
		return e
	}
	_, e = db.Exec(`INSERT OR IGNORE INTO auth_client_identity(id,backend_id,rp_id,logical_origin) VALUES(1,?,'localhost',?)`, id, origin)
	return e
}
func (s *Service) clientConfig(ctx context.Context, q querier) (out ClientConfig, err error) {
	out.APIVersion = "1.0.0"
	out.NativePasskey.ID = "default"
	out.LegacyBindings = []NativeBinding{}
	err = q.QueryRowContext(ctx, `SELECT backend_id,rp_id,logical_origin FROM auth_client_identity WHERE id=1`).Scan(&out.BackendID, &out.NativePasskey.RPID, &out.NativePasskey.Origin)
	if err != nil {
		return
	}
	p, e := s.passkeyDomains(ctx, q)
	if e != nil {
		return out, e
	}
	seen := map[string]bool{out.NativePasskey.RPID: true}
	for _, origin := range append([]string{s.Origin}, p.Origins...) {
		u, e := url.Parse(origin)
		if e != nil {
			return out, e
		}
		// Browser bindings are retained for domain credentials, never raw IP RPs.
		if u.Hostname() == "" || seen[u.Hostname()] || isIPHost(u.Hostname()) {
			continue
		}
		seen[u.Hostname()] = true
		out.LegacyBindings = append(out.LegacyBindings, NativeBinding{ID: Digest(origin), RPID: u.Hostname(), Origin: origin})
	}
	return
}
func (s *Service) ClientConfiguration(ctx context.Context) (ClientConfig, error) {
	return s.clientConfig(ctx, s.DB)
}
func (s *Service) nativeProof(ctx context.Context) *NativeProofBinding {
	p, _ := ctx.Value(nativeContextKey{}).(*NativeProofBinding)
	return p
}
func (s *Service) WithNativePasskey(ctx context.Context, id string) (context.Context, error) {
	cfg, e := s.clientConfig(ctx, s.DB)
	if e != nil {
		return nil, e
	}
	binding := cfg.NativePasskey
	if id != "" && id != "default" {
		found := false
		for _, b := range cfg.LegacyBindings {
			if b.ID == id {
				binding = b
				found = true
				break
			}
		}
		if !found {
			return nil, invalid("原生认证绑定已失效，请刷新后端配置")
		}
	}
	transport := s.passkeyOrigin(ctx)
	p := &NativeProofBinding{BackendID: cfg.BackendID, TransportOrigin: transport, Binding: binding}
	next := context.WithValue(ctx, nativeContextKey{}, p)
	if e = s.validateNativeBinding(next, s.DB); e != nil {
		return nil, e
	}
	return next, nil
}
func (s *Service) validateNativeBinding(ctx context.Context, q querier) error {
	p := s.nativeProof(ctx)
	if p == nil {
		return denied()
	}
	cfg, e := s.clientConfig(ctx, q)
	if e != nil {
		return e
	}
	if p.BackendID != cfg.BackendID {
		return denied()
	}
	found := p.Binding == cfg.NativePasskey
	for _, b := range cfg.LegacyBindings {
		found = found || p.Binding == b
	}
	domains, e := s.passkeyDomains(ctx, q)
	if e != nil {
		return e
	}
	approved := false
	for _, o := range append([]string{s.Origin}, domains.Origins...) {
		approved = approved || o == p.TransportOrigin
	}
	if !found || !approved {
		return denied()
	}
	return nil
}
func (s *Service) validNativeProof(ctx context.Context, stored *NativeProofBinding) bool {
	current := s.nativeProof(ctx)
	if current == nil || stored == nil {
		return current == nil && stored == nil
	}
	return *current == *stored
}
