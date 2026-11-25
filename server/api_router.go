// Copyright 2017 fatedier, fatedier@gmail.com
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

package server

import (
	"net"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	httppkg "github.com/fatedier/frp/pkg/util/http"
	"github.com/fatedier/frp/pkg/audit"
	"github.com/fatedier/frp/pkg/security"
	netpkg "github.com/fatedier/frp/pkg/util/net"
	adminapi "github.com/fatedier/frp/server/http"
)

func (svr *Service) registerRouteHandlers(helper *httppkg.RouterRegisterHelper) {
	helper.Router.HandleFunc("/healthz", healthz)
	subRouter := helper.Router.NewRoute().Subrouter()

	subRouter.Use(svr.panelGuard(helper.AuthMiddleware))
	subRouter.Use(httppkg.NewRequestLogger)

	// metrics
	if svr.cfg.EnablePrometheus {
		subRouter.Handle("/metrics", promhttp.Handler())
	}

	apiController := adminapi.NewController(svr.cfg, svr.clientRegistry, svr.pxyManager, svr.userStore)
	apiController.SetAudit(svr.audit)

	// apis
	subRouter.HandleFunc("/api/serverinfo", httppkg.MakeHTTPHandlerFunc(apiController.APIServerInfo)).Methods("GET")
	subRouter.HandleFunc("/api/proxy/{type}", httppkg.MakeHTTPHandlerFunc(apiController.APIProxyByType)).Methods("GET")
	subRouter.HandleFunc("/api/proxy/{type}/{name}", httppkg.MakeHTTPHandlerFunc(apiController.APIProxyByTypeAndName)).Methods("GET")
	subRouter.HandleFunc("/api/proxies/{name}", httppkg.MakeHTTPHandlerFunc(apiController.APIProxyByName)).Methods("GET")
	subRouter.HandleFunc("/api/traffic/{name}", httppkg.MakeHTTPHandlerFunc(apiController.APIProxyTraffic)).Methods("GET")
	subRouter.HandleFunc("/api/clients", httppkg.MakeHTTPHandlerFunc(apiController.APIClientList)).Methods("GET")
	subRouter.HandleFunc("/api/clients/{key}", httppkg.MakeHTTPHandlerFunc(apiController.APIClientDetail)).Methods("GET")
	subRouter.HandleFunc("/api/proxies", httppkg.MakeHTTPHandlerFunc(apiController.DeleteProxies)).Methods("DELETE")

	subRouter.HandleFunc("/api/v2/users", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2UserList)).Methods("GET")
	subRouter.HandleFunc("/api/v2/users", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2UserCreate)).Methods("POST")
	v2UserRouter := subRouter.NewRoute().Subrouter()
	v2UserRouter.UseEncodedPath()
	v2UserRouter.HandleFunc("/api/v2/users/{user}", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2UserUpdate)).Methods("PUT", "PATCH")
	v2UserRouter.HandleFunc("/api/v2/users/{user}", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2UserDelete)).Methods("DELETE")
	v2UserRouter.HandleFunc("/api/v2/users/{user}/token", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2UserResetToken)).Methods("POST")
	v2UserRouter.HandleFunc("/api/v2/users/{user}/traffic", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2UserResetTraffic)).Methods("DELETE", "POST")
	v2UserRouter.HandleFunc("/api/v2/users/{user}/install", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2UserInstall)).Methods("GET")
	subRouter.HandleFunc("/api/v2/audit", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2Audit)).Methods("GET")
	subRouter.HandleFunc("/api/v2/security/status", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2SecurityStatus)).Methods("GET")
	subRouter.HandleFunc("/api/v2/system/info", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2SystemInfo)).Methods("GET")
	subRouter.HandleFunc("/api/v2/system/prune", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2SystemPrune)).Methods("POST")
	subRouter.HandleFunc("/api/v2/clients", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2ClientList)).Methods("GET")
	v2EncodedPathRouter := subRouter.NewRoute().Subrouter()
	v2EncodedPathRouter.UseEncodedPath()
	v2EncodedPathRouter.HandleFunc("/api/v2/clients/{key}", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2ClientDetail)).Methods("GET")
	subRouter.HandleFunc("/api/v2/proxies", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2ProxyList)).Methods("GET")
	v2EncodedPathRouter.HandleFunc("/api/v2/proxies/{name}/traffic", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2ProxyTraffic)).Methods("GET")
	v2EncodedPathRouter.HandleFunc("/api/v2/proxies/{name}", httppkg.MakeHTTPHandlerFuncV2(apiController.APIV2ProxyDetail)).Methods("GET")

	// view: the dashboard shell is public (it shows its own login page and sends
	// the credentials with every API call); only the APIs are protected.
	helper.Router.Handle("/favicon.ico", http.FileServer(helper.AssetsFS)).Methods("GET")
	helper.Router.PathPrefix("/static/").Handler(
		netpkg.MakeHTTPGzipHandler(http.StripPrefix("/static/", http.FileServer(helper.AssetsFS))),
	).Methods("GET")

	helper.Router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/", http.StatusFound)
	})

	// onboarding: token based install scripts and the client binaries
	svr.registerPublicHandlers(helper.Router)
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(200)
}

// statusRecorder captures the status code written by the wrapped handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// panelGuard enforces the IP allow list and brute force ban on the dashboard and
// API, and records authentication failures.
func (svr *Service) panelGuard(authMW mux.MiddlewareFunc) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, _ := net.SplitHostPort(r.RemoteAddr)
			if err := svr.ban.Allow(ip); err != nil {
				http.Error(w, err.Error(), http.StatusTooManyRequests)
				return
			}
			if len(svr.cfg.Security.PanelIPAllowList) > 0 && !security.IPAllowed(ip, svr.cfg.Security.PanelIPAllowList) {
				http.Error(w, "panel access denied for this IP", http.StatusForbidden)
				return
			}
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			authMW(next).ServeHTTP(rec, r)
			if rec.status == http.StatusUnauthorized {
				svr.ban.Fail(ip)
				svr.audit.Write(audit.Event{Action: "panel-login", IP: ip, OK: false, Detail: "401"})
			} else if rec.status < 400 {
				svr.ban.Reset(ip)
			}
		})
	}
}
