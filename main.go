// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License. All rights reserved.

// Copyright (c) 2021 The BFE Authors.
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

package main

import (
	"flag"
	"fmt"
	_ "net/http/pprof"
	"path/filepath"
	"time"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"gopkg.in/tylerb/graceful.v1"

	"github.com/rainway-ai-gateway/ai-gateway-api/endpoints"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful/container/rdb"
	"github.com/rainway-ai-gateway/ai-gateway-api/version"
)

var (
	help       *bool   = flag.Bool("h", false, "to show help")
	showVer    *bool   = flag.Bool("v", false, "to show version")
	confDir    *string = flag.String("c", "./conf/", "API configure dir")
	serverConf *string = flag.String("sc", "ai_gateway_api.toml", "server conf file")

	logDir *string = flag.String("l", "./log", "dir path of log")
)

func main() {
	flag.Parse()

	if *help {
		flag.PrintDefaults()
		return
	}
	if *showVer {
		fmt.Printf("version %s\n", version.Version)
		return
	}

	if err := stateful.LoadConfig(filepath.Join(*confDir, *serverConf)); err != nil {
		stateful.Exit("LoadConfig", err, -1)
	}

	config := stateful.DefaultConfig
	config.LogDir = *logDir
	config.ConfigDir = *confDir

	config.Vars["conf_dir"] = *confDir
	config.Vars["log_dir"] = *logDir

	if err := config.Init(); err != nil {
		stateful.Exit("config.Init", err, -1)
	}

	if err := config.InitLog(); err != nil {
		stateful.Exit("config.InitLog", err, -1)
	}

	defer func() {
		time.Sleep(time.Second)
		stateful.CloseLog()
	}()

	if err := config.Depends.Init(); err != nil {
		stateful.Exit("config.Depends.Init", err, -1)
	}

	if err := config.InitDB(); err != nil {
		stateful.Exit("config.InitDB", err, -1)
	}

	// check redis server conf
	if config.RedisConf != nil {
		config.RedisConf.Init()
	}

	if err := rdb.Init(); err != nil {
		stateful.Exit("rdb.Init", err, -1)
	}

	// Fail-fast after the DB is up: encrypted secrets require a usable
	// keyring (see stateful.CheckSecretAtRest).
	if err := stateful.CheckSecretAtRest(); err != nil {
		stateful.Exit("CheckSecretAtRest", err, -1)
	}

	// A keyring plus a JSON-typed envelope column can only produce run-time
	// write failures (Error 3140), so refuse to start with an actionable
	// message instead (issue #264).
	if err := stateful.CheckEncryptedColumnTypes(); err != nil {
		stateful.Exit("CheckEncryptedColumnTypes", err, -1)
	}

	serverStartUp()
}

func serverStartUp() {
	serverConfig := stateful.DefaultConfig.Server

	if serverConfig.MonitorPort > 0 {
		stateful.NewMonitorServerWithRun(version.Version, serverConfig.MonitorPort)
	}

	n := negroni.New()
	router := mux.NewRouter()
	endpoints.RegisterRouters(router)
	n.UseHandler(router)

	timeout := time.Duration(serverConfig.GracefulTimeOutInMs) * time.Millisecond
	serverAddr := serverConfig.ServerAddr
	if serverAddr == "" {
		serverAddr = "0.0.0.0"
	}
	address := fmt.Sprintf("%s:%d", serverAddr, serverConfig.ServerPort)
	fmt.Println("Run Server At:", address)
	graceful.Run(address, timeout, n)
}
