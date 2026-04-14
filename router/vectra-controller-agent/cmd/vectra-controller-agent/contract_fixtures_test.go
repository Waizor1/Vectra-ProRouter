package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"vectra-controller-agent/internal/controlplane"
)

type contractFixtureCorpus struct {
	RouterJobs contractFixtureSet `json:"routerJobs"`
	JobResults contractFixtureSet `json:"jobResults"`
}

type contractFixtureSet struct {
	Accepted []namedFixture `json:"accepted"`
	Rejected []namedFixture `json:"rejected"`
}

type namedFixture struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

var fixtureUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestSharedJobContractFixturesDecodeInGo(t *testing.T) {
	corpus := loadContractFixtures(t)

	for _, fixture := range corpus.RouterJobs.Accepted {
		var job controlplane.Job
		if err := json.Unmarshal(fixture.Value, &job); err != nil {
			t.Fatalf("%s: decode accepted router job: %v", fixture.Name, err)
		}
		if err := validateFixtureJob(job); err != nil {
			t.Fatalf("%s: validate accepted router job: %v", fixture.Name, err)
		}
	}

	for _, fixture := range corpus.RouterJobs.Rejected {
		var job controlplane.Job
		err := json.Unmarshal(fixture.Value, &job)
		if err == nil {
			err = validateFixtureJob(job)
		}
		if err == nil {
			t.Fatalf("%s: expected rejected router job fixture to fail", fixture.Name)
		}
	}
}

func TestSharedJobResultContractFixturesDecodeInGo(t *testing.T) {
	corpus := loadContractFixtures(t)

	for _, fixture := range corpus.JobResults.Accepted {
		var request controlplane.JobResultRequest
		if err := json.Unmarshal(fixture.Value, &request); err != nil {
			t.Fatalf("%s: decode accepted job result: %v", fixture.Name, err)
		}
		if err := validateFixtureJobResult(request); err != nil {
			t.Fatalf("%s: validate accepted job result: %v", fixture.Name, err)
		}
	}

	for _, fixture := range corpus.JobResults.Rejected {
		var request controlplane.JobResultRequest
		err := json.Unmarshal(fixture.Value, &request)
		if err == nil {
			err = validateFixtureJobResult(request)
		}
		if err == nil {
			t.Fatalf("%s: expected rejected job result fixture to fail", fixture.Name)
		}
	}
}

func loadContractFixtures(t *testing.T) contractFixtureCorpus {
	t.Helper()

	path := filepath.Join(
		"..",
		"..",
		"..",
		"..",
		"packages",
		"contracts",
		"fixtures",
		"job-contract-fixtures.json",
	)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared contract fixtures: %v", err)
	}

	var corpus contractFixtureCorpus
	if err := json.Unmarshal(body, &corpus); err != nil {
		t.Fatalf("decode shared contract fixtures: %v", err)
	}

	return corpus
}

func validateFixtureJob(job controlplane.Job) error {
	if !fixtureUUIDPattern.MatchString(job.ID) {
		return errFixture("job id is not a uuid")
	}
	if _, err := time.Parse(time.RFC3339, job.CreatedAt); err != nil {
		return err
	}
	if !knownJobType(job.Type) {
		return errFixture("unknown job type")
	}
	if !knownJobState(job.State) {
		return errFixture("unknown job state")
	}
	if job.Payload == nil {
		return errFixture("payload is required")
	}

	switch job.Type {
	case "collect_router_logs":
		source := payloadString(job.Payload, "source")
		switch source {
		case "all", "controller", "passwall", "dnsmasq", "system":
		default:
			return errFixture("log collection fixture must use a known source")
		}
		if lines := payloadInt(job.Payload, "lines", 0); lines < 50 {
			return errFixture("log collection fixture must request at least 50 lines")
		}
	case "run_terminal_command":
		if payloadString(job.Payload, "command") == "" {
			return errFixture("terminal command fixture must include a command")
		}
		timeout := payloadInt(job.Payload, "timeoutSeconds", 0)
		if timeout < 5 || timeout > 120 {
			return errFixture("terminal command fixture timeout must stay within supported bounds")
		}
	case "update_controller":
		artifactJob := parseArtifactJob(job.Payload, []string{
			"vectra-controller-agent",
			"luci-app-vectra-controller",
		})
		return validateFixtureArtifactJob(artifactJob)
	case "update_passwall_packages":
		artifactJob := parseArtifactJob(job.Payload, []string{
			"luci-app-passwall2",
			"xray-core",
			"geoview",
		})
		return validateFixtureArtifactJob(artifactJob)
	case "validate_firmware":
		artifactJob := parseArtifactJob(job.Payload, nil)
		if artifactJob.ArtifactURL == "" || artifactJob.SHA256 == "" {
			return errFixture("firmware job requires artifact url and sha256")
		}
		if artifactJob.ValidationCommand == "" {
			return errFixture("firmware job requires validation command")
		}
	case "reconnect":
		if !payloadBool(job.Payload, "resumeProxy") || !payloadBool(job.Payload, "clearRescue") {
			return errFixture("reconnect fixture must exercise rescue clearing")
		}
	}

	return nil
}

func validateFixtureArtifactJob(job artifactJob) error {
	if len(job.PackageList) == 0 {
		return errFixture("artifact job package list is empty")
	}
	if len(job.PackageArtifacts) > 0 && len(job.PackageArtifacts) != len(job.PackageList) {
		return errFixture("explicit packageArtifacts must cover the package list")
	}
	if len(job.PackageArtifacts) == 0 && job.ArtifactURL == "" {
		return errFixture("artifact job requires explicit artifacts or feed artifactUrl")
	}
	return nil
}

func validateFixtureJobResult(request controlplane.JobResultRequest) error {
	if request.ProtocolVersion != controlplane.ProtocolVersion {
		return errFixture("protocol version mismatch")
	}
	if !fixtureUUIDPattern.MatchString(request.RouterID) {
		return errFixture("router id is not a uuid")
	}
	if !fixtureUUIDPattern.MatchString(request.JobID) {
		return errFixture("job id is not a uuid")
	}
	switch request.Status {
	case "accepted", "success", "failure":
	default:
		return errFixture("unknown job result status")
	}
	if request.Result == nil {
		return errFixture("result is required")
	}
	return nil
}

func knownJobType(value string) bool {
	switch value {
	case "apply_passwall_config",
		"refresh_subscriptions",
		"refresh_rules",
		"collect_router_logs",
		"run_terminal_command",
		"update_controller",
		"update_passwall_packages",
		"validate_firmware",
		"enter_direct_mode",
		"reconnect":
		return true
	default:
		return false
	}
}

func knownJobState(value string) bool {
	switch value {
	case "queued", "delivered", "running", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

type errFixture string

func (e errFixture) Error() string {
	return string(e)
}
