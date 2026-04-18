package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type helperInstaller struct {
	state         *helperStateStore
	fingerprinter hostFingerprinter
}

type authAttempt struct {
	profile  *credentialProfile
	password string
	source   string
}

type remoteVerification struct {
	Inventory struct {
		ControllerVersion string            `json:"controllerVersion"`
		PackageVersions   map[string]string `json:"packageVersions"`
		ServiceHealth     struct {
			Controller string `json:"controller"`
			Passwall   string `json:"passwall"`
			DNSMasq    string `json:"dnsmasq"`
		} `json:"serviceHealth"`
	} `json:"inventory"`
}

func (i *helperInstaller) RunInstall(session *installSession, origin string, request installRequest) {
	targetIP := request.TargetIP
	session.emitStage("helper detected", "success", "Локальный helper принял install-команду.", "", nil)

	fingerprint, reachable, err := i.fingerprinter.ProbeFingerprint(targetIP)
	if err != nil || !reachable {
		session.emitStage(
			"router found",
			"failure",
			fmt.Sprintf("Роутер %s не отвечает по SSH.", targetIP),
			"router_not_found",
			nil,
		)
		return
	}

	trustedFingerprint := i.state.trustedFingerprint(targetIP)
	if trustedFingerprint != "" && trustedFingerprint != fingerprint {
		session.emitStage(
			"router found",
			"failure",
			fmt.Sprintf("Host key mismatch для %s. Helper не продолжает установку с новым fingerprint без явного сброса доверия.", targetIP),
			"host_key_mismatch",
			nil,
		)
		return
	}

	routerMessage := fmt.Sprintf("Роутер %s найден. fingerprint=%s.", targetIP, fingerprint)
	if trustedFingerprint == "" {
		routerMessage += " Первый запуск пойдёт по TOFU и сохранит host key локально."
	}
	session.emitStage("router found", "success", routerMessage, "", nil)

	client, usedProfile, err := i.connectWithProfiles(targetIP, request)
	if err != nil {
		session.emitStage(
			"ssh authenticated",
			"failure",
			"Сохранённые credential profiles не подошли. Нужен пароль администратора роутера.",
			"auth_failed",
			nil,
		)
		return
	}
	defer client.Close()

	if trustedFingerprint == "" {
		_ = i.state.upsertTrustedHost(targetIP, fingerprint)
	}
	if usedProfile != nil {
		_ = i.state.markProfileUsed(usedProfile.ID)
	}
	if request.Password != "" && request.SaveProfile {
		if _, saveErr := i.state.saveProfile(targetIP, "root", request.Password); saveErr != nil {
			session.emitLog("ssh authenticated", "Не удалось сохранить профиль в secure storage: "+saveErr.Error())
		}
	}

	authMessage := "SSH авторизация прошла успешно."
	if usedProfile != nil {
		authMessage = fmt.Sprintf("SSH авторизация прошла через локальный профиль %s.", usedProfile.Label)
	} else if request.Password != "" {
		authMessage = "SSH авторизация прошла через вручную введённый пароль."
	}
	session.emitStage("ssh authenticated", "success", authMessage, "", nil)

	scriptURL := strings.TrimRight(origin, "/") + bootstrapScriptRel
	downloadCommand := fmt.Sprintf(
		"rm -f /tmp/vectra-ax3000t-bootstrap.sh && wget -O /tmp/vectra-ax3000t-bootstrap.sh %s",
		shellQuote(scriptURL),
	)
	if _, _, err := runRemoteCommandStreaming(client, "bootstrap downloaded", session, downloadCommand); err != nil {
		session.emitStage(
			"bootstrap downloaded",
			"failure",
			"Не удалось скачать публичный bootstrap-скрипт на роутер.",
			"bootstrap_failed",
			nil,
		)
		return
	}
	session.emitStage("bootstrap downloaded", "success", "Bootstrap-скрипт скачан на роутер.", "", nil)

	if _, _, err := runRemoteCommandStreaming(client, "packages installed", session, "sh /tmp/vectra-ax3000t-bootstrap.sh"); err != nil {
		session.emitStage(
			"packages installed",
			"failure",
			"Bootstrap завершился с ошибкой. Подробности смотрите в журнале ниже.",
			"bootstrap_failed",
			nil,
		)
		return
	}
	session.emitStage("packages installed", "success", "Bootstrap-команда завершилась без shell-ошибки.", "", nil)

	verification, verifyErr := i.verifyRemoteState(client, session)
	if verifyErr != nil {
		session.emitStage(
			"controller running",
			"failure",
			"Не удалось получить post-install статус с роутера.",
			"verification_failed",
			nil,
		)
		return
	}

	controllerChecklist := []checklistItem{
		{
			ID:      "controller-package",
			Label:   "vectra-controller-agent установлен",
			Status:  statusFromNonEmpty(verification.Inventory.ControllerVersion),
			Details: nonEmptyOr(verification.Inventory.ControllerVersion, "версия не определена"),
		},
		{
			ID:      "controller-service",
			Label:   "Controller service",
			Status:  statusFromExpected(verification.Inventory.ServiceHealth.Controller, "running"),
			Details: verification.Inventory.ServiceHealth.Controller,
		},
	}

	if verification.Inventory.ServiceHealth.Controller != "running" {
		session.emitStage(
			"controller running",
			"failure",
			"Bootstrap дошёл до проверки, но controller service не в running.",
			"verification_failed",
			controllerChecklist,
		)
		return
	}
	session.emitStage("controller running", "success", "Controller service работает.", "", controllerChecklist)

	passwallChecklist := []checklistItem{
		{
			ID:      "passwall-package",
			Label:   "luci-app-passwall2 установлен",
			Status:  statusFromNonEmpty(verification.Inventory.PackageVersions["luci-app-passwall2"]),
			Details: nonEmptyOr(verification.Inventory.PackageVersions["luci-app-passwall2"], "пакет не найден"),
		},
		{
			ID:      "passwall-service",
			Label:   "PassWall2 service",
			Status:  statusFromExpected(verification.Inventory.ServiceHealth.Passwall, "running"),
			Details: verification.Inventory.ServiceHealth.Passwall,
		},
		{
			ID:      "pending-review",
			Label:   "Новый роутер будет ждать review в панели",
			Status:  "success",
			Details: "Open global enrollment оставляет новый роутер в pending/import-review lane до операторской проверки.",
		},
	}

	if verification.Inventory.ServiceHealth.Passwall != "running" {
		session.emitStage(
			"passwall verified",
			"failure",
			"Controller поднялся, но PassWall2 не подтвердился как running.",
			"verification_failed",
			passwallChecklist,
		)
		return
	}
	session.emitStage("passwall verified", "success", "PassWall2 подтверждён как running.", "", passwallChecklist)
	session.emitStage("completed", "success", "Установка завершена. Роутер должен появиться в панели и ждать review.", "", nil)
}

func (i *helperInstaller) connectWithProfiles(targetIP string, request installRequest) (*ssh.Client, *credentialProfile, error) {
	attempts := make([]authAttempt, 0, 8)

	if request.Password != "" {
		attempts = append(attempts, authAttempt{
			password: request.Password,
			source:   "manual",
		})
	} else if request.CredentialProfileID != "" {
		profile, password, err := i.state.getProfilePassword(request.CredentialProfileID)
		if err == nil {
			attempts = append(attempts, authAttempt{
				profile:  &profile,
				password: password,
				source:   "profile",
			})
		}
	} else {
		for _, profile := range i.state.listProfiles() {
			loadedProfile, password, err := i.state.getProfilePassword(profile.ID)
			if err != nil {
				continue
			}
			attempts = append(attempts, authAttempt{
				profile:  &loadedProfile,
				password: password,
				source:   "profile",
			})
		}
	}

	for _, attempt := range attempts {
		client, err := i.connect(targetIP, attempt.password)
		if err == nil {
			return client, attempt.profile, nil
		}
	}

	return nil, nil, fmt.Errorf("authentication failed")
}

func (i *helperInstaller) connect(targetIP string, password string) (*ssh.Client, error) {
	trustedFingerprint := i.state.trustedFingerprint(targetIP)
	var observedFingerprint string

	config := &ssh.ClientConfig{
		User:    "root",
		Auth:    []ssh.AuthMethod{ssh.Password(password)},
		Timeout: 6 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			observedFingerprint = ssh.FingerprintSHA256(key)
			if trustedFingerprint != "" && trustedFingerprint != observedFingerprint {
				return fmt.Errorf("host key mismatch")
			}
			return nil
		},
	}

	client, err := ssh.Dial("tcp", net.JoinHostPort(targetIP, "22"), config)
	if err != nil {
		return nil, err
	}

	if trustedFingerprint == "" && observedFingerprint != "" {
		_ = i.state.upsertTrustedHost(targetIP, observedFingerprint)
	}

	return client, nil
}

func runRemoteCommandStreaming(client *ssh.Client, stage string, session *installSession, command string) (string, string, error) {
	sshSession, err := client.NewSession()
	if err != nil {
		return "", "", err
	}
	defer sshSession.Close()

	stdoutPipe, err := sshSession.StdoutPipe()
	if err != nil {
		return "", "", err
	}
	stderrPipe, err := sshSession.StderrPipe()
	if err != nil {
		return "", "", err
	}

	if err := sshSession.Start(command); err != nil {
		return "", "", err
	}

	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)

	stream := func(reader io.Reader, buffer *bytes.Buffer) {
		defer wg.Done()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			buffer.WriteString(line)
			buffer.WriteByte('\n')
			session.emitLog(stage, line)
		}
	}

	go stream(stdoutPipe, &stdoutBuffer)
	go stream(stderrPipe, &stderrBuffer)

	waitErr := sshSession.Wait()
	wg.Wait()
	return stdoutBuffer.String(), stderrBuffer.String(), waitErr
}

func (i *helperInstaller) verifyRemoteState(client *ssh.Client, session *installSession) (*remoteVerification, error) {
	command := "sh /usr/libexec/vectra-controller/render-config.sh /tmp/vectra-install-helper-status.json && cat /tmp/vectra-install-helper-status.json"
	stdout, _, err := runRemoteCommandStreaming(client, "passwall verified", session, command)
	if err != nil {
		return nil, err
	}

	var verification remoteVerification
	if err := json.Unmarshal([]byte(stdout), &verification); err != nil {
		return nil, err
	}

	return &verification, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func statusFromNonEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "failure"
	}
	return "success"
}

func statusFromExpected(value string, expected string) string {
	if value == expected {
		return "success"
	}
	return "failure"
}

func nonEmptyOr(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
