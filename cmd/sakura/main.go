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

// Command sakura is the combined server/client binary of Sakura Tunnel.
//
//	sakura            run the server (default)
//	sakura server     run the server
//	sakura client     run the client (same flags as frpc)
//	sakura version    print the version
//
// Shipping one executable keeps the download size down and lets the server host
// the very binary the tenant installs.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	frpscmd "github.com/fatedier/frp/cmd/frps"
	"github.com/fatedier/frp/cmd/frpc/sub"
	"github.com/fatedier/frp/pkg/brand"
	"github.com/fatedier/frp/pkg/util/system"
	"github.com/fatedier/frp/pkg/util/version"
	_ "github.com/fatedier/frp/pkg/webui"
)

func main() {
	system.EnableCompatibilityMode()

	// argv[0] based dispatch lets a renamed/copied binary run as a client.
	base := strings.ToLower(filepath.Base(os.Args[0]))
	clientByName := strings.Contains(base, "client") || strings.HasSuffix(base, "c.exe")

	args := os.Args[1:]
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "client", "c", "frpc":
			runClient(args[1:])
			return
		case "server", "s", "frps":
			runServer(args[1:])
			return
		case "version", "-version", "--version", "v":
			fmt.Println(version.Full())
			return
		}
	}
	if clientByName {
		runClient(args)
		return
	}
	runServer(args)
}

func runServer(args []string) {
	os.Args = append([]string{brand.Name}, args...)
	frpscmd.Execute()
}

func runClient(args []string) {
	os.Args = append([]string{brand.Name + " client"}, args...)
	sub.Execute()
}
