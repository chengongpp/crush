package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"

	mcptools "github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
	crushlog "github.com/charmbracelet/crush/internal/log"
	"github.com/charmbracelet/crush/internal/wecom"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	wecomBotID          string
	wecomBotSecret      string
	wecomWebSocketURL   string
	wecomThinkingPrompt string
)

var botCmd = &cobra.Command{
	Use:   "bot",
	Short: "Run bot integrations",
}

var botWeComCmd = &cobra.Command{
	Use:   "wecom",
	Short: "Run Crush as a WeCom bot over a WebSocket long connection",
	RunE: func(cmd *cobra.Command, _ []string) error {
		sigs := []os.Signal{os.Interrupt}
		sigs = append(sigs, addSignals(sigs)...)
		ctx, cancel := signal.NotifyContext(context.Background(), sigs...)
		defer cancel()
		cmd.SetContext(ctx)

		ws, cleanup, err := setupLocalWorkspace(cmd)
		if err != nil {
			return err
		}
		defer cleanup()

		appWs, ok := ws.(*workspace.AppWorkspace)
		if !ok {
			return fmt.Errorf("wecom bot only supports local workspace mode")
		}
		if !appWs.Config().IsConfigured() {
			return fmt.Errorf("no providers configured - please run 'crush' to set up a provider interactively")
		}

		cfg := resolveWeComBotConfig(appWs.Config().Bots.WeCom)
		if cfg.BotID == "" || cfg.Secret == "" {
			return fmt.Errorf("missing WeCom bot credentials - set bots.wecom.bot_id and bots.wecom.secret in crush.json or pass --bot-id/--secret")
		}

		if cfg.AutoApprovePermissions && !appWs.PermissionSkipRequests() {
			appWs.PermissionSetSkipRequests(true)
		}
		if !appWs.PermissionSkipRequests() {
			return fmt.Errorf("wecom bot requires unattended permissions - rerun with --yolo or set bots.wecom.auto_approve_permissions=true")
		}

		if err := mcptools.WaitForInit(ctx); err != nil {
			return fmt.Errorf("failed to initialize MCP clients: %w", err)
		}

		statePath := filepath.Join(appWs.Config().Options.DataDirectory, "wecom-bot-state.json")
		logFile := filepath.Join(appWs.Config().Options.DataDirectory, "logs", "crush.log")
		reporter := wecom.NewReporter(cmd.ErrOrStderr(), appWs.Config().Options.Debug)
		reporter.Startup(cfg, statePath, logFile)
		if !crushlog.Initialized() {
			crushlog.Setup(logFile, appWs.Config().Options.Debug, cmd.ErrOrStderr())
		}
		slog.Info("Starting WeCom bot bridge",
			"ws_url", cfg.WebSocketURL,
			"state_path", statePath,
			"log_file", logFile,
		)

		bot, err := wecom.NewBot(appWs, cfg, statePath, reporter)
		if err != nil {
			return err
		}

		return bot.Run(ctx)
	},
}

func init() {
	botWeComCmd.Flags().StringVar(&wecomBotID, "bot-id", "", "WeCom bot ID override")
	botWeComCmd.Flags().StringVar(&wecomBotSecret, "secret", "", "WeCom bot secret override")
	botWeComCmd.Flags().StringVar(&wecomWebSocketURL, "ws-url", "", "WeCom WebSocket endpoint override")
	botWeComCmd.Flags().StringVar(&wecomThinkingPrompt, "thinking-message", "", "Temporary message shown while Crush is generating a reply")
	botCmd.AddCommand(botWeComCmd)
}

func resolveWeComBotConfig(cfg *config.WeComConfig) config.WeComConfig {
	resolved := config.WeComConfig{}
	if cfg != nil {
		resolved = *cfg
	}
	if wecomBotID != "" {
		resolved.BotID = wecomBotID
	}
	if wecomBotSecret != "" {
		resolved.Secret = wecomBotSecret
	}
	if wecomWebSocketURL != "" {
		resolved.WebSocketURL = wecomWebSocketURL
	}
	if wecomThinkingPrompt != "" {
		resolved.ThinkingMessage = wecomThinkingPrompt
	}
	if resolved.WebSocketURL == "" {
		resolved.WebSocketURL = "wss://openws.work.weixin.qq.com"
	}
	if resolved.HeartbeatIntervalSeconds <= 0 {
		resolved.HeartbeatIntervalSeconds = 30
	}
	if resolved.ReconnectIntervalSeconds <= 0 {
		resolved.ReconnectIntervalSeconds = 1
	}
	if resolved.MaxReconnectAttempts == 0 {
		resolved.MaxReconnectAttempts = 10
	}
	if resolved.MaxAuthFailureAttempts == 0 {
		resolved.MaxAuthFailureAttempts = 5
	}
	if resolved.ThinkingMessage == "" {
		resolved.ThinkingMessage = "思考中..."
	}
	return resolved
}
