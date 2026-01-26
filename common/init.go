package common

import (
	"flag"
	"fmt"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"log"
	"os"
	"path/filepath"
)

var (
	Port         = flag.Int("port", 3000, "the listening port")
	PrintVersion = flag.Bool("version", false, "print version and exit")
	PrintHelp    = flag.Bool("help", false, "print help and exit")
	LogDir       = flag.String("log-dir", "", "specify the log directory")
)

func printHelp() {
	fmt.Println("One API " + Version + " - All in one API service for OpenAI API.")
	fmt.Println("Copyright (C) 2023 JustSong. All rights reserved.")
	fmt.Println("GitHub: https://github.com/songquanpeng/one-api")
	fmt.Println("Usage: one-api [--port <port>] [--log-dir <log directory>] [--version] [--help]")
}

func init() {
	flag.Parse()

	if *PrintVersion {
		fmt.Println(Version)
		os.Exit(0)
	}

	if *PrintHelp {
		printHelp()
		os.Exit(0)
	}

	if os.Getenv("SESSION_SECRET") != "" {
		if os.Getenv("SESSION_SECRET") == "random_string" {
			logger.SysError("SESSION_SECRET is set to an example value, please change it to a random string.")
		} else {
			config.SessionSecret = os.Getenv("SESSION_SECRET")
		}
	}
	if os.Getenv("SQLITE_PATH") != "" {
		SQLitePath = os.Getenv("SQLITE_PATH")
	}
	
	// 优先顺序：命令行参数 > 环境变量 > 默认值
	logDir := *LogDir
	if logDir == "" {
		logDir = os.Getenv("LOG_DIR")
	}
	if logDir == "" {
		logDir = "./logs" // 默认值
	}
	
	if logDir != "" {
		var err error
		logDir, err = filepath.Abs(logDir)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := os.Stat(logDir); os.IsNotExist(err) {
			err = os.Mkdir(logDir, 0777)
			if err != nil {
				log.Fatal(err)
			}
		}
		logger.LogDir = logDir
	}
}
