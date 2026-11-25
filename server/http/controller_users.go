// Copyright 2026 Sakura Tunnel Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package http

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/fatedier/frp/pkg/audit"
	"github.com/fatedier/frp/pkg/brand"
	"github.com/fatedier/frp/pkg/metrics/mem"
	"github.com/fatedier/frp/pkg/onetap"
	httppkg "github.com/fatedier/frp/pkg/util/http"
	"github.com/fatedier/frp/pkg/userdb"
	"github.com/fatedier/frp/server/http/model"
)

// userCreateReq is the body accepted by POST /api/v2/users.
type userCreateReq struct {
	User               string `json:"user"`
	MaxProxies         int    `json:"maxProxies"`
	BandwidthLimitKbps int    `json:"bandwidthLimitKbps"`
	TrafficQuotaBytes  int64  `json:"trafficQuotaBytes"`
	ExpireAt           int64    `json:"expireAt"`
	Note               string   `json:"note"`
	AllowIPs           []string `json:"allowIPs,omitempty"`
}

// userPatchReq is the body accepted by PUT /api/v2/users/{user}. Nil fields are
// left untouched.
type userPatchReq struct {
	Enabled            *bool   `json:"enabled"`
	MaxProxies         *int    `json:"maxProxies"`
	BandwidthLimitKbps *int    `json:"bandwidthLimitKbps"`
	TrafficQuotaBytes  *int64  `json:"trafficQuotaBytes"`
	ExpireAt           *int64    `json:"expireAt"`
	Note               *string   `json:"note"`
	AllowIPs           *[]string `json:"allowIPs,omitempty"`
}

func (c *Controller) requireStore() (*userdb.Store, error) {
	if c.userStore == nil {
		return nil, apiErr(400, "当前服务端未启用多用户模式，请在配置里设置 auth.method = \"user\"")
	}
	return c.userStore, nil
}

// apiErr builds a HTTP error carrying a readable Chinese message.
func apiErr(code int, format string, args ...any) *httppkg.Error {
	return httppkg.NewError(code, fmt.Sprintf(format, args...))
}

// APIV2UserList returns the tenants known by the user store together with their
// live traffic and connection counters.
func (c *Controller) APIV2UserList(ctx *httppkg.Context) (any, error) {
	page, pageSize, err := parseV2PageParams(ctx)
	if err != nil {
		return nil, err
	}

	store := c.userStore
	q := strings.ToLower(strings.TrimSpace(ctx.Query("q")))

	clientCounts := map[string]int{}
	if c.clientRegistry != nil {
		for _, info := range c.clientRegistry.List() {
			if !info.Online {
				continue
			}
			clientCounts[info.User]++
		}
	}
	proxyCounts := mem.StatsCollector.ProxyCountByUser()
	todayTraffic := mem.StatsCollector.TodayTrafficByUser()

	items := make([]model.V2UserResp, 0)
	if store == nil {
		// Single token mode: fall back to the runtime view so the dashboard still
		// shows something useful.
		type acc struct {
			clients int
			proxies int
		}
		agg := make(map[string]*acc)
		at := func(user string) *acc {
			a, ok := agg[user]
			if !ok {
				a = &acc{}
				agg[user] = a
			}
			return a
		}
		if c.clientRegistry != nil {
			for _, info := range c.clientRegistry.List() {
				at(info.User).clients++
			}
		}
		for _, ps := range c.listV2ProxyStats("") {
			at(ps.User).proxies++
		}
		for user, a := range agg {
			if q != "" && !strings.Contains(strings.ToLower(user), q) {
				continue
			}
			items = append(items, model.V2UserResp{
				User:        user,
				Online:      clientCounts[user] > 0,
				ClientCount: a.clients,
				ProxyCount:  a.proxies,
				Enabled:     true,
			})
		}
	} else {
		for _, u := range store.List() {
			if q != "" && !strings.Contains(strings.ToLower(u.Name), q) {
				continue
			}
			tt := todayTraffic[u.Name]
			items = append(items, model.V2UserResp{
				User:               u.Name,
				Online:             clientCounts[u.Name] > 0,
				ClientCount:        clientCounts[u.Name],
				ProxyCount:         proxyCounts[u.Name],
				TrafficIn:          u.TrafficIn,
				TrafficOut:         u.TrafficOut,
				TodayTrafficIn:     tt[0],
				TodayTrafficOut:    tt[1],
				BandwidthLimitKbps: u.BandwidthLimitKbps,
				TrafficQuotaBytes:  u.TrafficQuotaBytes,
				MaxProxies:         u.MaxProxies,
				Enabled:            u.Enabled,
				CreatedAt:          u.CreatedAt,
				ExpireAt:           u.ExpireAt,
				LastSeenAt:         u.LastSeenAt,
				Note:               u.Note,
				AllowIPs:           u.AllowIPs,
			})
		}
	}

	return buildV2PageResp(items, page, pageSize), nil
}

// APIV2UserCreate creates a tenant and returns its freshly generated token.
func (c *Controller) APIV2UserCreate(ctx *httppkg.Context) (any, error) {
	store, err := c.requireStore()
	if err != nil {
		return nil, err
	}
	var req userCreateReq
	if err := ctx.BindJSON(&req); err != nil {
		return nil, apiErr(400, "请求体解析失败: %v", err)
	}
	req.User = strings.TrimSpace(req.User)
	if req.User == "" {
		return nil, apiErr(400, "用户名不能为空")
	}
	if req.ExpireAt > 0 && req.ExpireAt < time.Now().Unix() {
		return nil, apiErr(400, "到期时间必须晚于当前时间")
	}

	u, err := store.Create(userdb.User{
		Name:               req.User,
		MaxProxies:         req.MaxProxies,
		BandwidthLimitKbps: req.BandwidthLimitKbps,
		TrafficQuotaBytes:  req.TrafficQuotaBytes,
		ExpireAt:           req.ExpireAt,
		Note:               req.Note,
		AllowIPs:           req.AllowIPs,
	})
	if err != nil {
		switch {
		case errors.Is(err, userdb.ErrExists):
			return nil, apiErr(409, "用户 %s 已存在", req.User)
		case errors.Is(err, userdb.ErrBadName):
			return nil, apiErr(400, "用户名只能是 2-32 位字母、数字、下划线、点或短横线")
		default:
			return nil, apiErr(500, "%v", err)
		}
	}
	return model.V2UserCreateResp{User: u.Name, Token: u.Token}, nil
}

// APIV2UserUpdate patches an existing tenant.
func (c *Controller) APIV2UserUpdate(ctx *httppkg.Context) (any, error) {
	store, err := c.requireStore()
	if err != nil {
		return nil, err
	}
	name := ctx.Param("user")
	var req userPatchReq
	if err := ctx.BindJSON(&req); err != nil {
		return nil, apiErr(400, "请求体解析失败: %v", err)
	}
	_, err = store.Update(name, userdb.Patch{
		Enabled:            req.Enabled,
		MaxProxies:         req.MaxProxies,
		BandwidthLimitKbps: req.BandwidthLimitKbps,
		TrafficQuotaBytes:  req.TrafficQuotaBytes,
		ExpireAt:           req.ExpireAt,
		Note:               req.Note,
		AllowIPs:           req.AllowIPs,
	})
	if err != nil {
		if errors.Is(err, userdb.ErrNotFound) {
			return nil, apiErr(404, "用户 %s 不存在", name)
		}
		return nil, apiErr(500, "%v", err)
	}
	return model.V2OKResp{OK: true}, nil
}

// APIV2UserDelete removes a tenant.
func (c *Controller) APIV2UserDelete(ctx *httppkg.Context) (any, error) {
	store, err := c.requireStore()
	if err != nil {
		return nil, err
	}
	name := ctx.Param("user")
	if err := store.Delete(name); err != nil {
		if errors.Is(err, userdb.ErrNotFound) {
			return nil, apiErr(404, "用户 %s 不存在", name)
		}
		return nil, apiErr(500, "%v", err)
	}
	return model.V2OKResp{OK: true}, nil
}

// APIV2UserResetToken rotates the access token of a tenant.
func (c *Controller) APIV2UserResetToken(ctx *httppkg.Context) (any, error) {
	store, err := c.requireStore()
	if err != nil {
		return nil, err
	}
	name := ctx.Param("user")
	token, err := store.ResetToken(name)
	if err != nil {
		if errors.Is(err, userdb.ErrNotFound) {
			return nil, apiErr(404, "用户 %s 不存在", name)
		}
		return nil, apiErr(500, "%v", err)
	}
	return model.V2UserCreateResp{User: name, Token: token}, nil
}

// APIV2UserResetTraffic clears the accumulated counters of a tenant.
func (c *Controller) APIV2UserResetTraffic(ctx *httppkg.Context) (any, error) {
	store, err := c.requireStore()
	if err != nil {
		return nil, err
	}
	name := ctx.Param("user")
	if err := store.ResetTraffic(name); err != nil {
		if errors.Is(err, userdb.ErrNotFound) {
			return nil, apiErr(404, "用户 %s 不存在", name)
		}
		return nil, apiErr(500, "%v", err)
	}
	return model.V2OKResp{OK: true}, nil
}

// APIV2UserInstall returns the ready-to-paste onboarding commands of a tenant.
func (c *Controller) APIV2UserInstall(ctx *httppkg.Context) (any, error) {
	store, err := c.requireStore()
	if err != nil {
		return nil, err
	}
	name := ctx.Param("user")
	u, ok := store.Get(name)
	if !ok {
		return nil, apiErr(404, "用户 %s 不存在", name)
	}

	host := strings.TrimSpace(ctx.Query("host"))
	if host == "" {
		host = ctx.Req.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" {
		host = "127.0.0.1"
	}

	scheme := "http"
	if ctx.Req.TLS != nil {
		scheme = "https"
	}
	if s := ctx.Query("scheme"); s == "https" || s == "http" {
		scheme = s
	}

	dashPort := c.serverCfg.WebServer.Port
	if dashPort == 0 {
		dashPort = c.serverCfg.BindPort
	}

	p := onetap.Params{
		Host:              host,
		Port:              c.serverCfg.BindPort,
		DashboardPort:     dashPort,
		Scheme:            scheme,
		User:              u.Name,
		Token:             u.Token,
		BinaryName:        brand.Name,
		DefaultRemotePort: 6000,
	}

	return model.V2UserInstallResp{
		User:    u.Name,
		Token:   u.Token,
		Linux:   p.LinuxCommand(),
		Windows: p.WindowsCommand(),
		Docker:  p.DockerCommand(),
		Downloads: map[string]string{
			"linux_amd64":   p.LinuxURL() + "?arch=amd64",
			"linux_arm64":   p.LinuxURL() + "?arch=arm64",
			"windows_amd64": p.WindowsURL(),
		},
	}, nil
}

func (c *Controller) userCount() int {
	if c.userStore == nil {
		return 0
	}
	return c.userStore.Count()
}

func (c *Controller) uptimeSec() int64 {
	if c.startedAt.IsZero() {
		return 0
	}
	return int64(time.Since(c.startedAt).Seconds())
}

// APIV2Audit returns the most recent audit events.
func (c *Controller) APIV2Audit(ctx *httppkg.Context) (any, error) {
	limit := 100
	if v := ctx.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	if c.auditLog == nil {
		return map[string]any{"items": []any{}, "enabled": false}, nil
	}
	events := c.auditLog.Tail(limit)
	q := strings.ToLower(strings.TrimSpace(ctx.Query("q")))
	if q != "" {
		filtered := make([]audit.Event, 0, len(events))
		for _, e := range events {
			if strings.Contains(strings.ToLower(e.User+" "+e.IP+" "+e.Action+" "+e.Detail), q) {
				filtered = append(filtered, e)
			}
		}
		events = filtered
	}
	return map[string]any{"items": events, "enabled": true, "path": c.auditLog.Path()}, nil
}

// APIV2SecurityStatus exposes the built-in protection settings.
func (c *Controller) APIV2SecurityStatus(ctx *httppkg.Context) (any, error) {
	sec := c.serverCfg.Security
	return map[string]any{
		"maxAuthFails":       sec.MaxAuthFails,
		"banMinutes":         sec.BanMinutes,
		"auditLog":           sec.AuditLog,
		"panelIPAllowList":   sec.PanelIPAllowList,
		"tlsForce":           c.serverCfg.Transport.TLS.Force,
	}, nil
}