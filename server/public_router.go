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

package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gorilla/mux"

	"github.com/fatedier/frp/pkg/brand"
	"github.com/fatedier/frp/pkg/onetap"
)

// serverBinaryName is the product name used for the client binary and the
// generated install commands.
const serverBinaryName = brand.Name

// registerPublicHandlers wires the unauthenticated onboarding endpoints:
//
//	/i/{token}   bash installer for one tenant
//	/w/{token}   PowerShell installer for one tenant
//	/dl/{name}   the self hosted client binary
//
// They are deliberately outside the dashboard auth middleware because the
// tenant only has a token, not the admin credentials. The token in the path is
// the secret.
func (svr *Service) registerPublicHandlers(r *mux.Router) {
	r.HandleFunc("/i/{token}", svr.handleLinuxScript).Methods("GET")
	r.HandleFunc("/w/{token}", svr.handleWindowsScript).Methods("GET")
	r.HandleFunc("/dl/{name}", svr.handleClientBinary).Methods("GET", "HEAD")
}

// installParams builds the onboarding parameters for the request host.
func (svr *Service) installParams(r *http.Request, token string) (onetap.Params, bool) {
	if svr.userStore == nil {
		return onetap.Params{}, false
	}
	u, ok := svr.userStore.AllByToken(token)
	if !ok {
		return onetap.Params{}, false
	}

	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" {
		host = "127.0.0.1"
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	dashPort := svr.cfg.WebServer.Port
	if dashPort == 0 {
		dashPort = svr.cfg.BindPort
	}

	return onetap.Params{
		Host:              host,
		Port:              svr.cfg.BindPort,
		DashboardPort:     dashPort,
		Scheme:            scheme,
		User:              u.Name,
		Token:             u.Token,
		BinaryName:        serverBinaryName,
		DefaultRemotePort: 6000,
	}, true
}

func (svr *Service) handleLinuxScript(w http.ResponseWriter, r *http.Request) {
	token := mux.Vars(r)["token"]
	p, ok := svr.installParams(r, token)
	if !ok {
		http.Error(w, "安装链接无效或已失效，请向管理员重新获取", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(p.LinuxScript()))
}

func (svr *Service) handleWindowsScript(w http.ResponseWriter, r *http.Request) {
	token := mux.Vars(r)["token"]
	p, ok := svr.installParams(r, token)
	if !ok {
		http.Error(w, "安装链接无效或已失效，请向管理员重新获取", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(p.PowerShellScript()))
}

func (svr *Service) handleClientBinary(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if !validClientBinaryName(name) {
		http.Error(w, "未知的客户端文件名", http.StatusNotFound)
		return
	}
	path, err := findClientBinary(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeFile(w, r, path)
}

func validClientBinaryName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}
	return true
}

// findClientBinary resolves a downloadable client binary. Operators drop the
// cross compiled clients in ./clients next to the server binary; when the
// requested target matches the running server, the server serves itself.
func findClientBinary(name string) (string, error) {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}

	candidates := []string{
		filepath.Join("clients", name),
	}
	if exeDir != "" {
		candidates = append(candidates,
			filepath.Join(exeDir, "clients", name),
			filepath.Join(exeDir, name),
		)
	}

	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}

	// Self service: the running binary answers for its own platform.
	selfName := fmt.Sprintf("%s-%s-%s", serverBinaryName, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		selfName += ".exe"
	}
	if strings.TrimSuffix(name, ".exe") == strings.TrimSuffix(selfName, ".exe") {
		if exe, err := os.Executable(); err == nil {
			return exe, nil
		}
	}

	return "", fmt.Errorf("客户端 %s 未部署，请把编译好的客户端放到服务端目录的 clients/ 子目录下", name)
}
