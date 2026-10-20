// Command imgen 是自托管的图像生成工作台。
//
//	imgen serve            # 默认子命令
//	imgen version
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/imgen/imgen/internal/app"
	"github.com/imgen/imgen/internal/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "imgen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}

	switch cmd {
	case "serve":
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		cfg, err = applyFlags(cfg, args)
		if err != nil {
			return err
		}
		return app.Run(cfg)

	case "version":
		fmt.Printf("imgen %s (built %s)\n", app.Version, app.BuildTime)
		return nil

	case "help", "-h", "--help":
		printUsage()
		return nil

	default:
		return fmt.Errorf("未知子命令 %q（可选：serve / version）", cmd)
	}
}

// applyFlags 处理少量常用开关；其余一律走环境变量，避免两套配置源。
func applyFlags(cfg config.Config, args []string) (config.Config, error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--addr":
			if i+1 >= len(args) {
				return cfg, errors.New("--addr 缺少取值")
			}
			i++
			cfg.Addr = args[i]
		case "--data":
			if i+1 >= len(args) {
				return cfg, errors.New("--data 缺少取值")
			}
			i++
			cfg.DataDir = args[i]
			cfg.DBPath = cfg.DataDir + "/imgen.db"
			cfg.BlobDir = cfg.DataDir + "/blobs"
			cfg.ThumbDir = cfg.DataDir + "/thumbs"
		default:
			return cfg, fmt.Errorf("未知参数 %q", args[i])
		}
	}
	return cfg, nil
}

func printUsage() {
	fmt.Print(`imgen · 自托管图像生成工作台

用法:
  imgen serve [--addr :8080] [--data ./data]
  imgen version

环境变量:
  IMGEN_ADDR              监听地址（默认 :8080）
  IMGEN_DATA              数据目录（默认 ./data）
  IMGEN_CONCURRENCY       全局并发上限 1..16（默认 2）
  IMGEN_MASTER_KEY        AES-GCM 主密钥 base64（留空则自动生成落盘）
  IMGEN_MAX_UPLOAD_MB     单张上传上限（默认 20）
  IMGEN_MAX_REMOTE_MB     远程图片下载上限（默认 32）
  IMGEN_KEEP_DAYS         历史保留天数（默认 30）
  IMGEN_DEBUG             非空则输出调试日志
`)
}
