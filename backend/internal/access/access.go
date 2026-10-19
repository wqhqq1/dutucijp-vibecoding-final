// Package access carries server-resolved country permissions. HTTP callers never
// choose this context. Store-only verification may continue to use simulated actors.
package access

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

type Profile struct {
	ID                 string   `json:"id"`
	Role               string   `json:"role"`
	DisplayName        string   `json:"display_name"`
	CountryScope       string   `json:"country_scope"`
	Countries          []string `json:"countries"`
	PermissionsVersion int      `json:"permissions_version"`
}

func (p Profile) All() bool { return p.Role == "admin" || p.CountryScope == "all" }
func (p Profile) Allows(country string) bool {
	if p.All() {
		return true
	}
	for _, c := range p.Countries {
		if c == country {
			return true
		}
	}
	return false
}
func (p Profile) Contains(other Profile) bool {
	if p.All() {
		return true
	}
	if other.All() {
		return false
	}
	for _, c := range other.Countries {
		if !p.Allows(c) {
			return false
		}
	}
	return true
}
func (p Profile) CanManageJobs() bool { return p.Role == "admin" || p.Role == "finance" && p.All() }
func (p Profile) SQL(column string) (string, []any) {
	if p.All() {
		return "", nil
	}
	if len(p.Countries) == 0 {
		return " AND 0", nil
	}
	args := make([]any, len(p.Countries))
	for i, c := range p.Countries {
		args[i] = c
	}
	return " AND " + column + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")", args
}

type key struct{}

func With(ctx context.Context, p Profile) context.Context { return context.WithValue(ctx, key{}, p) }
func From(ctx context.Context) (Profile, bool)            { p, ok := ctx.Value(key{}).(Profile); return p, ok }
func Global(ctx context.Context) bool                     { p, ok := From(ctx); return !ok || p.All() }
func Allows(ctx context.Context, country string) bool {
	p, ok := From(ctx)
	return !ok || p.Allows(country)
}
func SQL(ctx context.Context, column string) (string, []any) {
	p, ok := From(ctx)
	if !ok {
		return "", nil
	}
	return p.SQL(column)
}
func Version(ctx context.Context) int {
	p, ok := From(ctx)
	if !ok {
		return 0
	}
	return p.PermissionsVersion
}
func Compatible(ctx context.Context, version int) bool {
	v := Version(ctx)
	if version == 0 && v == 1 && Global(ctx) {
		return true
	}
	return v == version
}

type Querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func Load(ctx context.Context, q Querier, id string) (p Profile, err error) {
	err = q.QueryRowContext(ctx, `SELECT u.id,u.role,coalesce(nullif(p.display_name,''),u.id),coalesce(p.country_scope,'all'),coalesce(p.permissions_version,1) FROM auth_users u LEFT JOIN auth_account_profiles p ON p.user_id=u.id WHERE u.id=? AND u.state<>'deleted'`, id).Scan(&p.ID, &p.Role, &p.DisplayName, &p.CountryScope, &p.PermissionsVersion)
	if err != nil {
		return
	}
	p.Countries = []string{}
	if p.All() {
		p.CountryScope = "all"
		return
	}
	rows, e := q.QueryContext(ctx, `SELECT country FROM auth_user_countries WHERE user_id=? ORDER BY country`, id)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		if e = rows.Scan(&c); e != nil {
			return p, e
		}
		p.Countries = append(p.Countries, c)
	}
	return p, rows.Err()
}
func Canonical(countries []string) []string {
	out := append([]string{}, countries...)
	sort.Strings(out)
	result := []string{}
	for _, v := range out {
		if len(result) == 0 || result[len(result)-1] != v {
			result = append(result, v)
		}
	}
	return result
}
