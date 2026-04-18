package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/inventory"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/rescue"
	"vectra-controller-agent/internal/state"
)

var errControllerRestartRequested = errors.New("controller restart requested after self-update")

const skipControllerPostinstRestartEnv = "VECTRA_SKIP_POSTINST_RESTART"

var skipControllerPostinstRestartSentinelPath = "/tmp/vectra-skip-postinst-restart"

var defaultControllerPackageList = []string{
	"vectra-controller-agent",
	"luci-app-vectra-controller",
}

var defaultPasswallPackageList = []string{
	"tcping",
	"xray-core",
	"v2ray-geoip",
	"v2ray-geosite",
	"geoview",
	"chinadns-ng",
	"dnsmasq-full",
	"kmod-nft-socket",
	"kmod-nft-tproxy",
	"kmod-nft-nat",
	"luci-app-passwall2",
}

var passwallRuleRefreshAssets = []string{"geoip", "geosite"}

const passwallPostInstallRecoveryCommand = "/etc/init.d/passwall2 running >/dev/null 2>&1 && /etc/init.d/passwall2 restart || /etc/init.d/passwall2 start"

func main() {
	configPath := flag.String("config", "/etc/vectra-controller/config.json", "Path to JSON config file.")
	once := flag.Bool("once", true, "Run one loop iteration and exit.")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	persisted, err := state.Load(cfg.StatePath)
	if err != nil {
		log.Fatalf("load state: %v", err)
	}
	if err := state.EnsureIdentity(&persisted); err != nil {
		log.Fatalf("ensure identity: %v", err)
	}
	if err := state.Save(cfg.StatePath, persisted); err != nil {
		log.Fatalf("save state: %v", err)
	}
	clearControllerPostinstRestartSentinel()

	if cfg.RouterID == "" {
		cfg.RouterID = persisted.RouterID
	}
	if cfg.AgentToken == "" {
		cfg.AgentToken = persisted.AgentToken
	}

	client := controlplane.NewClient(controlplane.Options{
		BaseURL:    cfg.ControlURL,
		HTTPClient: nil,
		RouterID:   cfg.RouterID,
		AgentToken: cfg.AgentToken,
		Timeout:    cfg.RequestTimeout,
	})

	rescueState := persisted.Rescue.State
	if rescueState.Mode == "" {
		rescueState = rescue.State{
			Mode:             rescue.ModeProxy,
			LastTransitionAt: time.Now().Add(-cfg.Rescue.Cooldown),
		}
	} else if rescueState.LastTransitionAt.IsZero() {
		rescueState.LastTransitionAt = time.Now().Add(-cfg.Rescue.Cooldown)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if *once {
		if err := runOnce(ctx, &cfg, client, &rescueState, &persisted); err != nil && !errors.Is(err, errControllerRestartRequested) {
			log.Fatalf("run once: %v", err)
		}
		return
	}

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		if err := runOnce(ctx, &cfg, client, &rescueState, &persisted); err != nil {
			if errors.Is(err, errControllerRestartRequested) {
				return
			}
			log.Printf("run once failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runOnce(
	ctx context.Context,
	cfg *config.Config,
	client *controlplane.Client,
	rescueState *rescue.State,
	persisted *state.PersistedState,
) error {
	persistedBefore := *persisted
	collectedInventory := inventory.NewCollector().Collect(cfg.Inventory)
	collectedInventory.ProtocolVersion = controlplane.ProtocolVersion
	collectedInventory.PanelDomain = cfg.PanelURL
	if collectedInventory.PanelDomain == "" {
		collectedInventory.PanelDomain = cfg.ControlURL
	}
	if collectedInventory.DeviceIdentifier == "" {
		collectedInventory.DeviceIdentifier = persisted.DeviceIdentifier
	}
	if collectedInventory.DevicePublicKey == "" {
		collectedInventory.DevicePublicKey = persisted.DevicePublicKey
	}
	collectedInventory.AppliedRevisionID = persisted.AppliedRevisionID

	runtimeStatus := state.RuntimeStatus{
		ControlURL:        cfg.ControlURL,
		PanelURL:          cfg.PanelURL,
		RouterID:          cfg.RouterID,
		ControllerVersion: collectedInventory.ControllerVersion,
		ServiceState:      collectedInventory.ServiceHealth.Controller,
		RescueMode:        string(rescueState.Mode),
		SelectedNodeID:    collectedInventory.SelectedNodeID,
		SelectedNodeLabel: collectedInventory.SelectedNodeLabel,
		LastRescueReason:  persisted.Rescue.LastReason,
		PasswallEnabled:   collectedInventory.PasswallEnabled,
		AppliedRevisionID: persisted.AppliedRevisionID,
	}

	transitioned, health, rescueErr := evaluateLocalRescue(
		ctx,
		cfg,
		passwall.ExecBackend{},
		rescueState,
		persisted,
		&collectedInventory,
		&runtimeStatus,
	)
	if rescueErr != nil {
		runtimeStatus.LastError = rescueErr.Error()
		_ = state.SaveRuntimeStatus(cfg.StatusPath, runtimeStatus)
		if err := persistStateIfChanged(cfg.StatePath, persistedBefore, persisted); err != nil {
			return err
		}
		return fmt.Errorf("evaluate local rescue: %w", rescueErr)
	}
	if transitioned {
		collectedInventory = inventory.NewCollector().Collect(collectedInventory)
		collectedInventory.ProtocolVersion = controlplane.ProtocolVersion
		collectedInventory.PanelDomain = cfg.PanelURL
		if collectedInventory.PanelDomain == "" {
			collectedInventory.PanelDomain = cfg.ControlURL
		}
		collectedInventory.DeviceIdentifier = persisted.DeviceIdentifier
		collectedInventory.DevicePublicKey = persisted.DevicePublicKey
		collectedInventory.AppliedRevisionID = persisted.AppliedRevisionID
		applyRescueMetadata(persisted, rescueState, &collectedInventory, &runtimeStatus)
	}

	importSource := "check_in"
	if cfg.RouterID == "" || cfg.AgentToken == "" {
		importSource = "register"
	}
	passwallImport, importErr := buildPasswallImport(ctx, importSource)
	sendPasswallImport := false
	if importErr != nil {
		log.Printf("passwall import skipped: %v", importErr)
	} else if passwallImport != nil {
		collectedInventory.ConfigDigest = passwallImport.ConfigDigest
		runtimeStatus.ConfigDigest = passwallImport.ConfigDigest
		sendPasswallImport =
			importSource == "register" ||
				persisted.RequestImport ||
				persisted.LastImportedConfigDigest != passwallImport.ConfigDigest
		if !sendPasswallImport {
			passwallImport = nil
		}
	}

	if cfg.RouterID != "" && cfg.AgentToken != "" {
		if err := recoverJobJournal(
			ctx,
			cfg,
			client,
			persisted,
			collectedInventory,
		); err != nil {
			runtimeStatus.LastError = err.Error()
			_ = state.SaveRuntimeStatus(cfg.StatusPath, runtimeStatus)
			if persistErr := persistStateIfChanged(cfg.StatePath, persistedBefore, persisted); persistErr != nil {
				return persistErr
			}
			return fmt.Errorf("recover job journal: %w", err)
		}
	}

	if cfg.RouterID == "" || cfg.AgentToken == "" {
		registerResponse, err := client.Register(ctx, controlplane.RegisterRequest{
			ProtocolVersion: controlplane.ProtocolVersion,
			Inventory:       collectedInventory,
			PasswallImport:  passwallImport,
		})
		if err != nil {
			runtimeStatus.LastError = err.Error()
			_ = state.SaveRuntimeStatus(cfg.StatusPath, runtimeStatus)
			if persistErr := persistStateIfChanged(cfg.StatePath, persistedBefore, persisted); persistErr != nil {
				return persistErr
			}
			return fmt.Errorf("register: %w", err)
		}

		cfg.RouterID = registerResponse.RouterID
		cfg.AgentToken = registerResponse.IssuedToken
		persisted.RouterID = registerResponse.RouterID
		persisted.AgentToken = registerResponse.IssuedToken
		client.SetCredentials(registerResponse.RouterID, registerResponse.IssuedToken)
		runtimeStatus.RouterID = registerResponse.RouterID
		runtimeStatus.PendingApproval = registerResponse.PendingApproval
		runtimeStatus.ImportState = registerResponse.ConfigSyncState.ImportState
		runtimeStatus.LastOperatorMessage = registerResponse.OperatorMessage
		runtimeStatus.LastRegisterAt = time.Now().UTC().Format(time.RFC3339)
		runtimeStatus.LastError = ""
		if sendPasswallImport {
			persisted.LastImportedConfigDigest = collectedInventory.ConfigDigest
			persisted.RequestImport = false
		}
	}

	if cfg.DryRunPasswallProfile != nil {
		plan := passwall.BuildApplyPlan(*cfg.DryRunPasswallProfile, passwall.ApplyOptions{})
		log.Printf("generated passwall apply plan with %d operation(s)", len(plan.Operations))
		for _, op := range plan.Operations {
			log.Printf("operation[%s]: %s", op.Kind, op.Description)
		}
	}

	if cfg.RouterID == "" {
		runtimeStatus.LastError = "router_id is required for check-in"
		_ = state.SaveRuntimeStatus(cfg.StatusPath, runtimeStatus)
		if err := persistStateIfChanged(cfg.StatePath, persistedBefore, persisted); err != nil {
			return err
		}
		return fmt.Errorf("router_id is required for check-in")
	}

	checkInResponse, err := client.CheckIn(ctx, controlplane.CheckInRequest{
		ProtocolVersion: controlplane.ProtocolVersion,
		RouterID:        cfg.RouterID,
		Inventory:       collectedInventory,
		Health:          health,
		PasswallImport:  passwallImport,
	})
	if err != nil {
		runtimeStatus.LastError = err.Error()
		_ = state.SaveRuntimeStatus(cfg.StatusPath, runtimeStatus)
		if err := persistStateIfChanged(cfg.StatePath, persistedBefore, persisted); err != nil {
			return err
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("check-in timeout: %w", err)
		}
		return fmt.Errorf("check-in: %w", err)
	}

	runtimeStatus.LastCheckInAt = time.Now().UTC().Format(time.RFC3339)
	runtimeStatus.LastOperatorMessage = checkInResponse.OperatorMessage
	runtimeStatus.JobsAvailable = len(checkInResponse.Jobs)
	runtimeStatus.PendingApproval = checkInResponse.Status == "pending"
	runtimeStatus.ImportState = checkInResponse.ConfigSyncState.ImportState
	runtimeStatus.LastError = ""
	if sendPasswallImport {
		persisted.LastImportedConfigDigest = collectedInventory.ConfigDigest
	}
	persisted.RequestImport = checkInResponse.ConfigSyncState.RequestImport

	desiredRevision, decodeErr := decodeDesiredRevision(checkInResponse.DesiredRevision)
	if decodeErr != nil {
		log.Printf("desired revision decode failed: %v", decodeErr)
	}

	if err := executeJobs(
		ctx,
		cfg,
		client,
		checkInResponse.Jobs,
		desiredRevision,
		rescueState,
		persisted,
		&runtimeStatus,
	); err != nil {
		if errors.Is(err, errControllerRestartRequested) {
			applyRescueMetadata(persisted, rescueState, &collectedInventory, &runtimeStatus)
			if err := state.SaveRuntimeStatus(cfg.StatusPath, runtimeStatus); err != nil {
				return fmt.Errorf("persist runtime status: %w", err)
			}
			if err := persistStateIfChanged(cfg.StatePath, persistedBefore, persisted); err != nil {
				return err
			}
			return err
		}
		runtimeStatus.LastError = err.Error()
		_ = state.SaveRuntimeStatus(cfg.StatusPath, runtimeStatus)
		if err := persistStateIfChanged(cfg.StatePath, persistedBefore, persisted); err != nil {
			return err
		}
		return err
	}
	if len(checkInResponse.Jobs) > 0 {
		collectedInventory = inventory.NewCollector().Collect(collectedInventory)
		collectedInventory.ProtocolVersion = controlplane.ProtocolVersion
		collectedInventory.PanelDomain = cfg.PanelURL
		if collectedInventory.PanelDomain == "" {
			collectedInventory.PanelDomain = cfg.ControlURL
		}
		collectedInventory.DeviceIdentifier = persisted.DeviceIdentifier
		collectedInventory.DevicePublicKey = persisted.DevicePublicKey
		collectedInventory.AppliedRevisionID = persisted.AppliedRevisionID
	}
	applyRescueMetadata(persisted, rescueState, &collectedInventory, &runtimeStatus)
	if err := state.SaveRuntimeStatus(cfg.StatusPath, runtimeStatus); err != nil {
		return fmt.Errorf("persist runtime status: %w", err)
	}
	if err := persistStateIfChanged(cfg.StatePath, persistedBefore, persisted); err != nil {
		return err
	}
	return nil
}

func scheduleControllerRestart(backend commandRunner) {
	restartCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := backend.Run(
		restartCtx,
		"sh",
		"-c",
		"(sleep 2; /etc/init.d/vectra-controller restart >/tmp/vectra-controller-self-update.log 2>&1) &",
	); err != nil {
		log.Printf("schedule controller restart after self-update failed: %v", err)
	}
}

func buildPasswallImport(ctx context.Context, source string) (*controlplane.PasswallImportedState, error) {
	imported, err := passwall.NewImporter(passwall.ExecBackend{}).Import(ctx, source)
	if err != nil {
		return nil, err
	}

	configPayload, err := json.Marshal(imported.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal imported config: %w", err)
	}

	return &controlplane.PasswallImportedState{
		Config:       configPayload,
		RawSnapshot:  imported.RawSnapshot,
		ConfigDigest: imported.ConfigDigest,
		ImportedAt:   imported.ImportedAt,
		Source:       imported.Source,
	}, nil
}

func decodeDesiredRevision(raw json.RawMessage) (*controlplane.DesiredRevisionSummary, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var revision controlplane.DesiredRevisionSummary
	if err := json.Unmarshal(raw, &revision); err != nil {
		return nil, err
	}
	return &revision, nil
}

func persistCurrentJob(
	path string,
	persisted *state.PersistedState,
	job controlplane.Job,
) error {
	if persisted == nil {
		return nil
	}

	current := state.CurrentJob{
		JobID:      job.ID,
		JobType:    job.Type,
		AcceptedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if job.Type == "update_controller" {
		artifactJob := parseArtifactJob(job.Payload, []string{
			"vectra-controller-agent",
			"luci-app-vectra-controller",
		})
		current.ExpectedControllerVersion = artifactJob.ArtifactVersion
	}

	persisted.CurrentJob = current
	return state.Save(path, *persisted)
}

func persistPendingJobResult(
	path string,
	persisted *state.PersistedState,
	request controlplane.JobResultRequest,
) error {
	if persisted == nil {
		return nil
	}

	requestCopy := request
	persisted.PendingJobResult = &requestCopy
	return state.Save(path, *persisted)
}

func clearJobJournal(path string, persisted *state.PersistedState) error {
	if persisted == nil {
		return nil
	}

	persisted.CurrentJob = state.CurrentJob{}
	persisted.PendingJobResult = nil
	return state.Save(path, *persisted)
}

func flushPendingJobResult(
	ctx context.Context,
	cfg *config.Config,
	client *controlplane.Client,
	persisted *state.PersistedState,
	inventory controlplane.RouterInventory,
) error {
	if persisted == nil || persisted.PendingJobResult == nil {
		return nil
	}

	request := *persisted.PendingJobResult
	if request.ProtocolVersion == "" {
		request.ProtocolVersion = controlplane.ProtocolVersion
	}
	if request.RouterID == "" {
		request.RouterID = cfg.RouterID
	}

	if persisted.CurrentJob.JobType == "update_controller" &&
		request.Status == "success" &&
		persisted.CurrentJob.ExpectedControllerVersion != "" {
		if inventory.ControllerVersion == "" {
			return fmt.Errorf(
				"controller restart confirmation pending: runtime controller version unavailable",
			)
		}
		if inventory.ControllerVersion != persisted.CurrentJob.ExpectedControllerVersion {
			return fmt.Errorf(
				"controller restart confirmation pending: got %s want %s",
				inventory.ControllerVersion,
				persisted.CurrentJob.ExpectedControllerVersion,
			)
		}
		if request.Result == nil {
			request.Result = map[string]interface{}{}
		}
		request.Result["confirmedControllerVersion"] = inventory.ControllerVersion
	}

	if _, err := client.SubmitJobResult(ctx, request); err != nil {
		return fmt.Errorf("submit pending job result: %w", err)
	}

	return clearJobJournal(cfg.StatePath, persisted)
}

func submitJobResultNow(
	ctx context.Context,
	cfg *config.Config,
	client *controlplane.Client,
	persisted *state.PersistedState,
	request controlplane.JobResultRequest,
	inventory controlplane.RouterInventory,
) error {
	if request.ProtocolVersion == "" {
		request.ProtocolVersion = controlplane.ProtocolVersion
	}
	if request.RouterID == "" {
		request.RouterID = cfg.RouterID
	}
	if err := persistPendingJobResult(cfg.StatePath, persisted, request); err != nil {
		return fmt.Errorf("persist pending job result: %w", err)
	}
	if err := flushPendingJobResult(ctx, cfg, client, persisted, inventory); err != nil {
		return err
	}
	return nil
}

func recoverJobJournal(
	ctx context.Context,
	cfg *config.Config,
	client *controlplane.Client,
	persisted *state.PersistedState,
	inventory controlplane.RouterInventory,
) error {
	if persisted == nil {
		return nil
	}

	if persisted.PendingJobResult != nil {
		return flushPendingJobResult(ctx, cfg, client, persisted, inventory)
	}

	if persisted.CurrentJob.JobID == "" {
		return nil
	}

	return submitJobResultNow(
		ctx,
		cfg,
		client,
		persisted,
		controlplane.JobResultRequest{
			ProtocolVersion: controlplane.ProtocolVersion,
			RouterID:        cfg.RouterID,
			JobID:           persisted.CurrentJob.JobID,
			Status:          "failure",
			Result: map[string]interface{}{
				"error":       "agent restarted before terminal job result",
				"jobType":     persisted.CurrentJob.JobType,
				"acceptedAt":  persisted.CurrentJob.AcceptedAt,
				"restartSafe": persisted.CurrentJob.JobType == "update_controller",
			},
		},
		inventory,
	)
}

func executeJobs(
	ctx context.Context,
	cfg *config.Config,
	client *controlplane.Client,
	jobs []controlplane.Job,
	desiredRevision *controlplane.DesiredRevisionSummary,
	rescueState *rescue.State,
	persisted *state.PersistedState,
	runtimeStatus *state.RuntimeStatus,
) error {
	backend := passwall.ExecBackend{}
	executor := passwall.NewExecutor(backend)
	for _, job := range jobs {
		if _, err := client.SubmitJobResult(ctx, controlplane.JobResultRequest{
			ProtocolVersion: controlplane.ProtocolVersion,
			RouterID:        cfg.RouterID,
			JobID:           job.ID,
			Status:          "accepted",
			Result:          map[string]interface{}{"message": "job accepted"},
		}); err != nil {
			return fmt.Errorf("ack job %s: %w", job.ID, err)
		}
		if err := persistCurrentJob(cfg.StatePath, persisted, job); err != nil {
			return fmt.Errorf("persist current job %s: %w", job.ID, err)
		}

		switch job.Type {
		case "apply_passwall_config":
			if desiredRevision == nil {
				if err := submitFailure(ctx, client, cfg, persisted, job.ID, "", "", "desired revision payload missing", map[string]interface{}{"error": "desired revision missing"}); err != nil {
					return err
				}
				continue
			}

			result, err := executor.Apply(ctx, desiredRevision.Config, passwall.ApplyOptions{
				RefreshSubscriptions: desiredRevision.Impact.RefreshSubscriptions,
				RefreshRules:         desiredRevision.Impact.RefreshRules,
				RestartService:       desiredRevision.Impact.RequiresRestart,
			})
			if err != nil {
				if submitErr := submitFailure(ctx, client, cfg, persisted, job.ID, "", "", err.Error(), map[string]interface{}{"error": err.Error()}); submitErr != nil {
					return submitErr
				}
				continue
			}

			persisted.AppliedRevisionID = desiredRevision.ID
			persisted.ConfigDigest = result.ConfigDigest
			runtimeStatus.AppliedRevisionID = desiredRevision.ID
			runtimeStatus.ConfigDigest = result.ConfigDigest

			postApplyImportDigest := ""
			if imported, importErr := buildPasswallImport(ctx, "check_in"); importErr != nil {
				log.Printf("post-apply import skipped: %v", importErr)
			} else if imported != nil {
				postApplyImportDigest = imported.ConfigDigest
				persisted.ConfigDigest = imported.ConfigDigest
				persisted.LastImportedConfigDigest = imported.ConfigDigest
				persisted.RequestImport = false
				runtimeStatus.ConfigDigest = imported.ConfigDigest
			}

			stdout, stderr := summarizeCommandResults(result.CommandResults)
			if err := submitJobResultNow(ctx, cfg, client, persisted, controlplane.JobResultRequest{
				ProtocolVersion:   controlplane.ProtocolVersion,
				RouterID:          cfg.RouterID,
				JobID:             job.ID,
				Status:            "success",
				AppliedRevisionID: desiredRevision.ID,
				ConfigDigest:      result.ConfigDigest,
				Stdout:            stdout,
				Stderr:            stderr,
				Result: map[string]interface{}{
					"configDigest":          result.ConfigDigest,
					"noop":                  len(result.Plan.Operations) == 0 && len(result.UCICommands) == 0 && len(result.CommandResults) == 0,
					"uciCommands":           result.UCICommands,
					"operationResults":      result.Plan.Operations,
					"commandResults":        result.CommandResults,
					"postApplyImportDigest": postApplyImportDigest,
				},
			}, controlplane.RouterInventory{}); err != nil {
				return fmt.Errorf("submit apply result: %w", err)
			}
		case "refresh_subscriptions":
			result, err := backend.Run(ctx, "lua", "/usr/share/passwall2/subscribe.lua", "start", "all")
			result = passwall.NormalizeCommandResult(result)
			if err != nil {
				if submitErr := submitFailure(ctx, client, cfg, persisted, job.ID, result.Stdout, result.Stderr, err.Error(), map[string]interface{}{"error": err.Error()}); submitErr != nil {
					return submitErr
				}
				continue
			}
			if err := submitJobResultNow(ctx, cfg, client, persisted, controlplane.JobResultRequest{
				ProtocolVersion: controlplane.ProtocolVersion,
				RouterID:        cfg.RouterID,
				JobID:           job.ID,
				Status:          "success",
				Stdout:          result.Stdout,
				Stderr:          result.Stderr,
				Result: map[string]interface{}{
					"command": result.Command,
				},
			}, controlplane.RouterInventory{}); err != nil {
				return fmt.Errorf("submit refresh subscriptions result: %w", err)
			}
		case "refresh_rules":
			assets := "geoip,geosite"
			if desiredRevision != nil && len(desiredRevision.Config.RuleManage.EnabledAssets) > 0 {
				assets = strings.Join(desiredRevision.Config.RuleManage.EnabledAssets, ",")
			}
			result, err := backend.Run(ctx, "lua", "/usr/share/passwall2/rule_update.lua", "log", assets)
			if err != nil {
				if submitErr := submitFailure(ctx, client, cfg, persisted, job.ID, result.Stdout, result.Stderr, err.Error(), map[string]interface{}{"error": err.Error(), "assets": assets}); submitErr != nil {
					return submitErr
				}
				continue
			}
			if err := submitJobResultNow(ctx, cfg, client, persisted, controlplane.JobResultRequest{
				ProtocolVersion: controlplane.ProtocolVersion,
				RouterID:        cfg.RouterID,
				JobID:           job.ID,
				Status:          "success",
				Stdout:          result.Stdout,
				Stderr:          result.Stderr,
				Result: map[string]interface{}{
					"command": result.Command,
					"assets":  assets,
				},
			}, controlplane.RouterInventory{}); err != nil {
				return fmt.Errorf("submit refresh rules result: %w", err)
			}
		case "collect_router_logs":
			logRequest := parseCollectRouterLogsJob(job.Payload)
			snapshots, stdout, stderr, err := collectRouterLogs(ctx, backend, logRequest)
			resultPayload := buildRouterLogResultPayload(
				logRequest,
				snapshots,
				stdout,
				stderr,
			)
			if err != nil {
				if submitErr := submitFailure(
					ctx,
					client,
					cfg,
					persisted,
					job.ID,
					stdout,
					stderr,
					err.Error(),
					resultPayload,
				); submitErr != nil {
					return submitErr
				}
				continue
			}
			if err := submitJobResultNow(ctx, cfg, client, persisted, controlplane.JobResultRequest{
				ProtocolVersion: controlplane.ProtocolVersion,
				RouterID:        cfg.RouterID,
				JobID:           job.ID,
				Status:          "success",
				Stdout:          stdout,
				Stderr:          stderr,
				Result:          resultPayload,
			}, controlplane.RouterInventory{}); err != nil {
				return fmt.Errorf("submit collect router logs result: %w", err)
			}
		case "run_terminal_command":
			terminalRequest := parseRunTerminalCommandJob(job.Payload)
			execution := executeTerminalCommand(ctx, terminalRequest)
			resultPayload := buildTerminalCommandResultPayload(execution)
			if execution.ExecutionFailure != nil {
				if submitErr := submitFailure(
					ctx,
					client,
					cfg,
					persisted,
					job.ID,
					execution.Stdout,
					execution.Stderr,
					execution.ExecutionFailure.Error(),
					resultPayload,
				); submitErr != nil {
					return submitErr
				}
				continue
			}
			if err := submitJobResultNow(ctx, cfg, client, persisted, controlplane.JobResultRequest{
				ProtocolVersion: controlplane.ProtocolVersion,
				RouterID:        cfg.RouterID,
				JobID:           job.ID,
				Status:          "success",
				Stdout:          execution.Stdout,
				Stderr:          execution.Stderr,
				Result:          resultPayload,
			}, controlplane.RouterInventory{}); err != nil {
				return fmt.Errorf("submit terminal command result: %w", err)
			}
		case "enter_direct_mode":
			if err := backend.Batch(ctx, []string{
				"set passwall2.@global[0].enabled='0'",
				"commit passwall2",
			}); err != nil {
				if submitErr := submitFailure(ctx, client, cfg, persisted, job.ID, "", "", err.Error(), map[string]interface{}{"error": err.Error()}); submitErr != nil {
					return submitErr
				}
				continue
			}
			result, err := backend.Run(ctx, "/etc/init.d/passwall2", "restart")
			if err != nil {
				if submitErr := submitFailure(ctx, client, cfg, persisted, job.ID, result.Stdout, result.Stderr, err.Error(), map[string]interface{}{"error": err.Error(), "enteredDirectMode": true}); submitErr != nil {
					return submitErr
				}
				continue
			}
			if err := submitJobResultNow(ctx, cfg, client, persisted, controlplane.JobResultRequest{
				ProtocolVersion: controlplane.ProtocolVersion,
				RouterID:        cfg.RouterID,
				JobID:           job.ID,
				Status:          "success",
				Stdout:          result.Stdout,
				Stderr:          result.Stderr,
				IncidentTransitions: []map[string]interface{}{
					{
						"type":   "entered_direct_mode",
						"state":  "open",
						"reason": "Router entered direct mode via operator job.",
					},
				},
				Result: map[string]interface{}{
					"enteredDirectMode": true,
					"command":           result.Command,
					"reason":            "Router entered direct mode via operator job.",
				},
			}, controlplane.RouterInventory{}); err != nil {
				return fmt.Errorf("submit direct mode result: %w", err)
			}
			rescueState.Mode = rescue.ModeDirect
			rescueState.ProxyFailureCount = 0
			rescueState.DirectSuccessCount = 0
			rescueState.ProxySuccessCount = 0
			rescueState.LastTransitionAt = time.Now().UTC()
			persisted.Rescue.State = *rescueState
			persisted.Rescue.LastMode = string(rescue.ModeDirect)
			persisted.Rescue.LastReason = "Router entered direct mode via operator job."
			persisted.Rescue.HappenedAt = time.Now().UTC().Format(time.RFC3339)
			runtimeStatus.RescueMode = string(rescue.ModeDirect)
			runtimeStatus.LastRescueReason = persisted.Rescue.LastReason
			runtimeStatus.PasswallEnabled = false
		case "reconnect":
			if payloadBool(job.Payload, "resumeProxy") || payloadBool(job.Payload, "clearRescue") {
				recoveryReason := "Proxy mode restored via operator job."
				if err := resumeProxyMode(
					ctx,
					backend,
					rescueState,
					persisted,
					runtimeStatus,
					time.Now().UTC(),
				); err != nil {
					if submitErr := submitFailure(
						ctx,
						client,
						cfg,
						persisted,
						job.ID,
						"",
						"",
						err.Error(),
						map[string]interface{}{
							"error":          err.Error(),
							"resumeProxy":    true,
							"clearRescue":    payloadBool(job.Payload, "clearRescue"),
							"recoveredProxy": false,
						},
					); submitErr != nil {
						return submitErr
					}
					continue
				}
				if err := submitJobResultNow(ctx, cfg, client, persisted, controlplane.JobResultRequest{
					ProtocolVersion: controlplane.ProtocolVersion,
					RouterID:        cfg.RouterID,
					JobID:           job.ID,
					Status:          "success",
					IncidentTransitions: []map[string]interface{}{
						{
							"type":   "recovered",
							"state":  "resolved",
							"reason": recoveryReason,
						},
					},
					Result: map[string]interface{}{
						"message":        "Reconnect job restored proxy mode and cleared active rescue state.",
						"recoveredProxy": true,
						"reason":         recoveryReason,
					},
				}, controlplane.RouterInventory{}); err != nil {
					return fmt.Errorf("submit reconnect recovery result: %w", err)
				}
				continue
			}
			if err := submitJobResultNow(ctx, cfg, client, persisted, controlplane.JobResultRequest{
				ProtocolVersion: controlplane.ProtocolVersion,
				RouterID:        cfg.RouterID,
				JobID:           job.ID,
				Status:          "success",
				Result: map[string]interface{}{
					"message": "Reconnect job acknowledged; next polling loop will continue with current credentials.",
				},
			}, controlplane.RouterInventory{}); err != nil {
				return fmt.Errorf("submit reconnect result: %w", err)
			}
		case "update_controller":
			artifactJob := parseArtifactJob(job.Payload, defaultControllerPackageList)
			if len(artifactJob.PackageArtifacts) > 0 || artifactJob.ArtifactURL != "" {
				if err := runStagedPackageInstallJob(
					ctx,
					client,
					cfg,
					persisted,
					job.ID,
					backend,
					artifactJob,
					true,
					true,
					false,
				); err != nil {
					return err
				}
				continue
			}
			if err := runPackageInstallJob(
				ctx,
				client,
				cfg,
				persisted,
				job.ID,
				backend,
				artifactJob.PackageList,
				true,
				true,
				false,
			); err != nil {
				return err
			}
		case "update_passwall_packages":
			artifactJob := parseArtifactJob(job.Payload, defaultPasswallPackageList)
			if err := runPasswallPackageUpdateJob(
				ctx,
				client,
				cfg,
				persisted,
				job.ID,
				backend,
				artifactJob,
			); err != nil {
				return err
			}
		case "validate_firmware":
			artifactJob := parseArtifactJob(job.Payload, nil)
			if artifactJob.ArtifactURL == "" {
				imagePath := payloadString(job.Payload, "imagePath")
				if imagePath == "" {
					if err := submitFailure(ctx, client, cfg, persisted, job.ID, "", "", "firmware artifactUrl or legacy imagePath payload is required", map[string]interface{}{"error": "missing firmware artifact"}); err != nil {
						return err
					}
					continue
				}
				result, err := backend.Run(ctx, "sysupgrade", "-T", imagePath)
				if err != nil {
					if submitErr := submitFailure(ctx, client, cfg, persisted, job.ID, result.Stdout, result.Stderr, err.Error(), map[string]interface{}{"error": err.Error(), "imagePath": imagePath}); submitErr != nil {
						return submitErr
					}
					continue
				}
				if err := submitJobResultNow(ctx, cfg, client, persisted, controlplane.JobResultRequest{
					ProtocolVersion: controlplane.ProtocolVersion,
					RouterID:        cfg.RouterID,
					JobID:           job.ID,
					Status:          "success",
					Stdout:          result.Stdout,
					Stderr:          result.Stderr,
					Result: map[string]interface{}{
						"validated": true,
						"imagePath": imagePath,
						"command":   result.Command,
					},
				}, controlplane.RouterInventory{}); err != nil {
					return fmt.Errorf("submit validate firmware result: %w", err)
				}
				continue
			}

			staged, stageErr := stageArtifact(
				ctx,
				artifactJob.ArtifactURL,
				artifactJob.SHA256,
				artifactJob.SignatureURL,
				cfg.RequestTimeout,
			)
			if stageErr != nil {
				if err := submitFailure(ctx, client, cfg, persisted, job.ID, "", "", stageErr.Error(), map[string]interface{}{
					"artifactUrl":     artifactJob.ArtifactURL,
					"artifactVersion": artifactJob.ArtifactVersion,
				}); err != nil {
					return err
				}
				continue
			}

			validationCommand := strings.TrimSpace(artifactJob.ValidationCommand)
			if validationCommand == "" {
				validationCommand = "sysupgrade -T /tmp/firmware.bin"
			}
			validationCommand = strings.ReplaceAll(validationCommand, "/tmp/firmware.bin", staged.Path)

			result, err := backend.Run(ctx, "sh", "-c", validationCommand)
			if err != nil {
				if submitErr := submitFailure(ctx, client, cfg, persisted, job.ID, result.Stdout, result.Stderr, err.Error(), map[string]interface{}{
					"error":           err.Error(),
					"artifactUrl":     artifactJob.ArtifactURL,
					"artifactVersion": artifactJob.ArtifactVersion,
					"stagedPath":      staged.Path,
				}); submitErr != nil {
					return submitErr
				}
				continue
			}
			if err := submitJobResultNow(ctx, cfg, client, persisted, controlplane.JobResultRequest{
				ProtocolVersion: controlplane.ProtocolVersion,
				RouterID:        cfg.RouterID,
				JobID:           job.ID,
				Status:          "success",
				Stdout:          result.Stdout,
				Stderr:          result.Stderr,
				Result: map[string]interface{}{
					"validated":         true,
					"artifactUrl":       artifactJob.ArtifactURL,
					"artifactVersion":   artifactJob.ArtifactVersion,
					"stagedPath":        staged.Path,
					"checksumSha256":    artifactJob.SHA256,
					"signatureVerified": staged.SignaturePath != "",
					"validationCommand": validationCommand,
					"command":           result.Command,
				},
			}, controlplane.RouterInventory{}); err != nil {
				return fmt.Errorf("submit validate firmware result: %w", err)
			}
		default:
			if err := submitFailure(ctx, client, cfg, persisted, job.ID, "", "", "job type not implemented on agent yet", map[string]interface{}{"error": "unsupported job type", "jobType": job.Type}); err != nil {
				return err
			}
		}
	}

	return nil
}

func summarizeCommandResults(results []passwall.CommandResult) (string, string) {
	stdoutLines := make([]string, 0, len(results))
	stderrLines := make([]string, 0, len(results))
	for _, result := range results {
		if result.Stdout != "" {
			stdoutLines = append(stdoutLines, result.Command+": "+result.Stdout)
		}
		if result.Stderr != "" {
			stderrLines = append(stderrLines, result.Command+": "+result.Stderr)
		}
	}
	return strings.Join(stdoutLines, "\n"), strings.Join(stderrLines, "\n")
}

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) (passwall.CommandResult, error)
}

func runPackageInstallJob(
	ctx context.Context,
	client *controlplane.Client,
	cfg *config.Config,
	persisted *state.PersistedState,
	jobID string,
	backend commandRunner,
	packages []string,
	restartController bool,
	forceReinstall bool,
	repairPasswall bool,
) error {
	args := []string{"install"}
	if forceReinstall {
		args = append(args, "--force-reinstall")
	}
	args = append(args, packages...)
	results, err := executePackageInstallSequence(
		ctx,
		backend,
		args,
		restartController,
		repairPasswall,
		true,
	)
	stdout, stderr := collectCommandOutputs(results)

	resultPayload := map[string]interface{}{
		"packages": packages,
		"commands": collectCommands(results),
	}
	if repairPasswall {
		resultPayload["postInstallRepair"] = true
		resultPayload["ruleRefreshAssets"] = append([]string(nil), passwallRuleRefreshAssets...)
		resultPayload["postInstallCommands"] = collectPostInstallCommands(results)
	}
	if err != nil {
		return submitFailure(ctx, client, cfg, persisted, jobID, stdout, stderr, err.Error(), resultPayload)
	}

	request := controlplane.JobResultRequest{
		ProtocolVersion: controlplane.ProtocolVersion,
		RouterID:        cfg.RouterID,
		JobID:           jobID,
		Status:          "success",
		Stdout:          stdout,
		Stderr:          stderr,
		Result:          resultPayload,
	}
	if err := persistPendingJobResult(cfg.StatePath, persisted, request); err != nil {
		return fmt.Errorf("persist package install result: %w", err)
	}

	if restartController {
		scheduleControllerRestart(backend)
		return errControllerRestartRequested
	}

	if err := flushPendingJobResult(ctx, cfg, client, persisted, controlplane.RouterInventory{}); err != nil {
		return fmt.Errorf("submit package install result: %w", err)
	}

	return nil
}

func runStagedPackageInstallJob(
	ctx context.Context,
	client *controlplane.Client,
	cfg *config.Config,
	persisted *state.PersistedState,
	jobID string,
	backend commandRunner,
	job artifactJob,
	restartController bool,
	forceReinstall bool,
	repairPasswall bool,
) error {
	stagedArtifacts, err := stagePackageArtifacts(ctx, job, cfg.RequestTimeout)
	if err != nil {
		return submitFailure(ctx, client, cfg, persisted, jobID, "", "", err.Error(), map[string]interface{}{
			"error":           err.Error(),
			"artifactUrl":     job.ArtifactURL,
			"artifactVersion": job.ArtifactVersion,
			"packages":        job.PackageList,
		})
	}

	args := []string{"install"}
	if forceReinstall {
		args = append(args, "--force-reinstall")
	}
	for _, artifact := range stagedArtifacts {
		args = append(args, artifact.Path)
	}

	results, err := executePackageInstallSequence(
		ctx,
		backend,
		args,
		restartController,
		repairPasswall,
		false,
	)
	stdout, stderr := collectCommandOutputs(results)

	resultPayload := map[string]interface{}{
		"packages":        job.PackageList,
		"channel":         job.Channel,
		"artifactUrl":     job.ArtifactURL,
		"artifactVersion": job.ArtifactVersion,
		"artifacts":       summarizeStagedArtifacts(stagedArtifacts),
		"commands":        collectCommands(results),
	}
	if repairPasswall {
		resultPayload["postInstallRepair"] = true
		resultPayload["ruleRefreshAssets"] = append([]string(nil), passwallRuleRefreshAssets...)
		resultPayload["postInstallCommands"] = collectPostInstallCommands(results)
	}
	if err != nil {
		return submitFailure(ctx, client, cfg, persisted, jobID, stdout, stderr, err.Error(), resultPayload)
	}

	request := controlplane.JobResultRequest{
		ProtocolVersion: controlplane.ProtocolVersion,
		RouterID:        cfg.RouterID,
		JobID:           jobID,
		Status:          "success",
		Stdout:          stdout,
		Stderr:          stderr,
		Result:          resultPayload,
	}
	if err := persistPendingJobResult(cfg.StatePath, persisted, request); err != nil {
		return fmt.Errorf("persist staged package install result: %w", err)
	}

	if restartController {
		scheduleControllerRestart(backend)
		return errControllerRestartRequested
	}

	if err := flushPendingJobResult(ctx, cfg, client, persisted, controlplane.RouterInventory{}); err != nil {
		return fmt.Errorf("submit staged package install result: %w", err)
	}

	return nil
}

func executePackageInstallSequence(
	ctx context.Context,
	backend commandRunner,
	installArgs []string,
	suppressControllerPostinstRestart bool,
	repairPasswall bool,
	refreshPackageIndex bool,
) ([]passwall.CommandResult, error) {
	results := make([]passwall.CommandResult, 0, 4)

	if refreshPackageIndex {
		updateResult, err := backend.Run(ctx, "opkg", "update")
		results = append(results, updateResult)
		if err != nil {
			return results, err
		}
	}

	installResult, err := runOpkgInstall(
		ctx,
		backend,
		installArgs,
		suppressControllerPostinstRestart,
	)
	results = append(results, installResult)
	if err != nil {
		return results, err
	}

	if repairPasswall {
		repairResults, err := runPasswallPostInstallRepair(ctx, backend)
		results = append(results, repairResults...)
		if err != nil {
			return results, err
		}
	}

	return results, nil
}

func runOpkgInstall(
	ctx context.Context,
	backend commandRunner,
	installArgs []string,
	suppressControllerPostinstRestart bool,
) (passwall.CommandResult, error) {
	if !suppressControllerPostinstRestart {
		return backend.Run(ctx, "opkg", installArgs...)
	}

	cleanupSentinel, err := createControllerPostinstRestartSentinel()
	if err != nil {
		return passwall.CommandResult{}, err
	}
	defer cleanupSentinel()

	return backend.Run(
		ctx,
		"sh",
		"-c",
		buildEnvWrappedCommand(
			skipControllerPostinstRestartEnv,
			"1",
			"opkg",
			installArgs,
		),
	)
}

func buildEnvWrappedCommand(
	envName string,
	envValue string,
	name string,
	args []string,
) string {
	commandParts := []string{
		envName + "=" + shellQuote(envValue),
		shellQuote(name),
	}
	for _, arg := range args {
		commandParts = append(commandParts, shellQuote(arg))
	}
	return strings.Join(commandParts, " ")
}

func createControllerPostinstRestartSentinel() (func(), error) {
	if skipControllerPostinstRestartSentinelPath == "" {
		return func() {}, nil
	}

	if err := os.WriteFile(
		skipControllerPostinstRestartSentinelPath,
		[]byte(time.Now().UTC().Format(time.RFC3339)+"\n"),
		0o600,
	); err != nil {
		return nil, fmt.Errorf("write controller self-update sentinel: %w", err)
	}

	return func() {
		if err := os.Remove(skipControllerPostinstRestartSentinelPath); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			log.Printf("clear controller self-update sentinel: %v", err)
		}
	}, nil
}

func clearControllerPostinstRestartSentinel() {
	if skipControllerPostinstRestartSentinelPath == "" {
		return
	}

	if err := os.Remove(skipControllerPostinstRestartSentinelPath); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		log.Printf("clear stale controller self-update sentinel: %v", err)
	}
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}

	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func runPasswallPostInstallRepair(
	ctx context.Context,
	backend commandRunner,
) ([]passwall.CommandResult, error) {
	results := make([]passwall.CommandResult, 0, 2)

	ruleRefreshResult, err := backend.Run(
		ctx,
		"lua",
		"/usr/share/passwall2/rule_update.lua",
		"log",
		strings.Join(passwallRuleRefreshAssets, ","),
	)
	results = append(results, ruleRefreshResult)
	if err != nil {
		return results, err
	}

	serviceResult, err := backend.Run(ctx, "sh", "-c", passwallPostInstallRecoveryCommand)
	results = append(results, serviceResult)
	if err != nil {
		return results, err
	}

	return results, nil
}

func collectCommandOutputs(results []passwall.CommandResult) (string, string) {
	stdoutLines := make([]string, 0, len(results))
	stderrLines := make([]string, 0, len(results))
	for _, result := range results {
		if result.Stdout != "" {
			stdoutLines = append(stdoutLines, result.Stdout)
		}
		if result.Stderr != "" {
			stderrLines = append(stderrLines, result.Stderr)
		}
	}

	return strings.TrimSpace(strings.Join(stdoutLines, "\n")), strings.TrimSpace(strings.Join(stderrLines, "\n"))
}

func collectCommands(results []passwall.CommandResult) []string {
	commands := make([]string, 0, len(results))
	for _, result := range results {
		if result.Command != "" {
			commands = append(commands, result.Command)
		}
	}
	return commands
}

func collectPostInstallCommands(results []passwall.CommandResult) []string {
	if len(results) <= 2 {
		return nil
	}

	return collectCommands(results[2:])
}

func summarizeStagedArtifacts(artifacts []stagedArtifact) []map[string]interface{} {
	summary := make([]map[string]interface{}, 0, len(artifacts))
	for _, artifact := range artifacts {
		summary = append(summary, map[string]interface{}{
			"url":            artifact.URL,
			"sha256":         artifact.SHA256,
			"signaturePath":  artifact.SignaturePath,
			"stagedFileName": filepath.Base(artifact.Path),
		})
	}
	return summary
}

func payloadString(payload map[string]interface{}, key string) string {
	if payload == nil {
		return ""
	}
	if value, ok := payload[key].(string); ok {
		return value
	}
	return ""
}

func payloadBool(payload map[string]interface{}, key string) bool {
	if payload == nil {
		return false
	}
	value, ok := payload[key]
	if !ok {
		return false
	}
	boolValue, ok := value.(bool)
	return ok && boolValue
}

func payloadStringSlice(payload map[string]interface{}, key string) []string {
	if payload == nil {
		return nil
	}
	raw, ok := payload[key]
	if !ok {
		return nil
	}
	switch value := raw.(type) {
	case []string:
		return value
	case []interface{}:
		items := make([]string, 0, len(value))
		for _, entry := range value {
			if item, ok := entry.(string); ok && item != "" {
				items = append(items, item)
			}
		}
		return items
	default:
		return nil
	}
}

func payloadInt64(payload map[string]interface{}, key string) int64 {
	if payload == nil {
		return 0
	}

	raw, ok := payload[key]
	if !ok {
		return 0
	}

	switch value := raw.(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	default:
		return 0
	}
}

func submitFailure(
	ctx context.Context,
	client *controlplane.Client,
	cfg *config.Config,
	persisted *state.PersistedState,
	jobID string,
	stdout string,
	stderr string,
	message string,
	result map[string]interface{},
) error {
	err := submitJobResultNow(ctx, cfg, client, persisted, controlplane.JobResultRequest{
		ProtocolVersion: controlplane.ProtocolVersion,
		RouterID:        cfg.RouterID,
		JobID:           jobID,
		Status:          "failure",
		Stdout:          stdout,
		Stderr:          stderr,
		Result: func() map[string]interface{} {
			if result == nil {
				return map[string]interface{}{"error": message}
			}
			result["error"] = message
			return result
		}(),
	}, controlplane.RouterInventory{})
	if err != nil {
		return fmt.Errorf("submit failure result: %w", err)
	}
	return nil
}

func persistStateIfChanged(
	path string,
	before state.PersistedState,
	current *state.PersistedState,
) error {
	if current == nil || before == *current {
		return nil
	}

	if err := state.Save(path, *current); err != nil {
		return fmt.Errorf("persist state: %w", err)
	}
	return nil
}

func init() {
	// Guard to avoid accidental execution when imported in integration tests.
	if os.Getenv("VECTRA_AGENT_NO_MAIN") == "1" {
		panic("VECTRA_AGENT_NO_MAIN must not be set for runtime")
	}
}
