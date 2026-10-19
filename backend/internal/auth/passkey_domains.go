package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

type PasskeyDomains struct {
	BaseOrigin string   `json:"base_origin"`
	Origins    []string `json:"origins"`
	Version    int      `json:"version"`
}
type originContextKey struct{}

// WithPasskeyOrigin is set by the trusted HTTP boundary, never by form data.
func WithPasskeyOrigin(ctx context.Context, origin string) context.Context {
	return context.WithValue(ctx, originContextKey{}, origin)
}
func (s *Service) passkeyOrigin(ctx context.Context) string {
	if v, ok := ctx.Value(originContextKey{}).(string); ok {
		return v
	}
	return s.Origin
}
func NormalizePasskeyOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, e := url.Parse(raw)
	if e != nil || u == nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return "", invalid("请填写完整 HTTPS 入口，不含路径、参数、通配符或用户信息")
	}
	host, e := idna.Lookup.ToASCII(strings.ToLower(u.Hostname()))
	if e != nil || host == "" || len(host) > 253 || strings.HasSuffix(host, ".") || net.ParseIP(host) != nil {
		return "", invalid("请使用有效域名，不能使用 IP 地址")
	}
	if host != "localhost" {
		suffix, _ := publicsuffix.PublicSuffix(host)
		if !strings.Contains(host, ".") || suffix == host {
			return "", invalid("请使用完整站点域名，不能使用公共后缀")
		}
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", invalid("域名格式不正确")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", invalid("域名格式不正确")
			}
		}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && (scheme != "http" || host != "localhost") {
		return "", invalid("外网域名必须使用 HTTPS；只有 localhost 可使用 HTTP")
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return "", invalid("端口格式不正确")
	}
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", invalid("端口须为1至65535")
		}
		port = strconv.Itoa(n)
		if scheme == "https" && port == "443" || scheme == "http" && port == "80" {
			port = ""
		}
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, nil
}
func (s *Service) passkeyDomains(ctx context.Context, q querier) (out PasskeyDomains, err error) {
	out.BaseOrigin = s.Origin
	var raw string
	err = q.QueryRowContext(ctx, `SELECT origins_json,version FROM auth_passkey_domains WHERE id=1`).Scan(&raw, &out.Version)
	if err == nil {
		err = decode(raw, &out.Origins)
	}
	return
}
func (s *Service) PasskeyDomains(ctx context.Context, token string) (PasskeyDomains, error) {
	v, e := s.Current(ctx, token, true)
	if e != nil {
		return PasskeyDomains{}, e
	}
	if v.Role != "admin" {
		return PasskeyDomains{}, forbidden()
	}
	return s.passkeyDomains(ctx, s.DB)
}
func (s *Service) UpdatePasskeyDomains(ctx context.Context, token string, input PasskeyDomains) (out PasskeyDomains, err error) {
	err = s.atomic(ctx, func(c *sql.Conn) error {
		actor, e := s.manager(ctx, c, token, "admin")
		if e != nil {
			return e
		}
		before, e := s.passkeyDomains(ctx, c)
		if e != nil {
			return e
		}
		if input.Version != before.Version {
			return conflict("域名设置已变化，请刷新后重试")
		}
		if len(input.Origins) > 20 {
			return invalid("最多允许20个额外入口")
		}
		unique := map[string]bool{}
		out = PasskeyDomains{BaseOrigin: s.Origin, Version: before.Version + 1, Origins: []string{}}
		for _, raw := range input.Origins {
			origin, e := NormalizePasskeyOrigin(raw)
			if e != nil {
				return e
			}
			if origin == s.Origin {
				continue
			}
			if !unique[origin] {
				unique[origin] = true
				out.Origins = append(out.Origins, origin)
			}
		}
		sort.Strings(out.Origins)
		current := s.passkeyOrigin(ctx)
		if current != s.Origin && !unique[current] {
			return invalid("当前正在使用的入口不能移除，请先切换到本机或其他已批准入口")
		}
		if _, e = c.ExecContext(ctx, `UPDATE auth_passkey_domains SET origins_json=?,version=? WHERE id=1`, encode(out.Origins), out.Version); e != nil {
			return e
		}
		if _, e = c.ExecContext(ctx, `DELETE FROM auth_challenges`); e != nil {
			return e
		}
		return s.managementAudit(ctx, c, actor.ID, "passkey_domains", "Passkey域名", "", before, out)
	})
	if err != nil {
		out = PasskeyDomains{}
	}
	return
}

// ApprovedOrigins returns a copy; failed reads never fall back to arbitrary hosts.
func (s *Service) ApprovedOrigins(ctx context.Context) ([]string, error) {
	p, e := s.passkeyDomains(ctx, s.DB)
	if e != nil {
		return nil, e
	}
	return append([]string{s.Origin}, p.Origins...), nil
}
func (s *Service) passkeyWA(ctx context.Context, q querier) (*webauthn.WebAuthn, string, error) {
	origin := s.passkeyOrigin(ctx)
	p, e := s.passkeyDomains(ctx, q)
	if e != nil {
		return nil, "", e
	}
	approved := origin == s.Origin
	for _, v := range p.Origins {
		approved = approved || origin == v
	}
	if !approved {
		return nil, "", denied()
	}
	u, e := url.Parse(origin)
	if e != nil {
		return nil, "", e
	}
	rp := u.Hostname()
	wa, e := webauthn.New(&webauthn.Config{RPDisplayName: "退货对账与差异处理系统", RPID: rp, RPOrigins: []string{origin}, AuthenticatorSelection: protocol.AuthenticatorSelection{UserVerification: protocol.VerificationRequired}})
	return wa, rp, e
}

type scopedCredential struct {
	webauthn.Credential
	RPID string `json:"app_rp_id,omitempty"`
}

// Credential has its own JSON decoder; explicitly decode our sibling metadata.
func (c *scopedCredential) UnmarshalJSON(raw []byte) error {
	if e := json.Unmarshal(raw, &c.Credential); e != nil {
		return e
	}
	var metadata struct {
		RPID string `json:"app_rp_id"`
	}
	if e := json.Unmarshal(raw, &metadata); e != nil {
		return e
	}
	c.RPID = metadata.RPID
	return nil
}

func encodeCredential(c *webauthn.Credential, rp string) string {
	return encode(scopedCredential{*c, rp})
}
func filterCredentials(ctx context.Context, q querier, a account, rp string) (account, error) {
	rows, e := q.QueryContext(ctx, `SELECT credential_json FROM auth_credentials WHERE user_id=? ORDER BY id`, a.ID)
	if e != nil {
		return a, e
	}
	defer rows.Close()
	a.Credentials = nil
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return a, e
		}
		var c scopedCredential
		if e = decode(raw, &c); e != nil {
			return a, e
		}
		if c.RPID == "" {
			c.RPID = "localhost"
		}
		if c.RPID == rp {
			a.Credentials = append(a.Credentials, c.Credential)
		}
	}
	return a, rows.Err()
}

type passkeySession struct {
	webauthn.SessionData
	Origin string `json:"app_origin,omitempty"`
}

func (s *Service) validChallengeOrigin(ctx context.Context, origin string) bool {
	if origin == "" {
		origin = s.Origin
	}
	return origin == s.passkeyOrigin(ctx)
}
