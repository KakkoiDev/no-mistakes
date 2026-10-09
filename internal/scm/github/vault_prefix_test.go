package github

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/scm"
)

// Automic Vault writes this line ahead of gh's own output once a human approves
// the call. Which stream it lands on is not known here, so every case runs
// with the line on stdout and on stderr; CombinedOutput call sites see both,
// Output call sites only stdout.
const vaultLine = "automic vault: human approval required\n"

type vaultStream string

const (
	vaultOnStdout vaultStream = "stdout"
	vaultOnStderr vaultStream = "stderr"
)

// vaultCmdFactory answers each gh command from responses, prefixing the Vault
// line to the commands listed in noisy. The helper process writes the Vault
// line before the JSON, matching the order gh's wrapper emits them in.
func vaultCmdFactory(stream vaultStream, noisy map[string]bool, responses map[string]string) CmdFactory {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		key := strings.TrimSpace(name + " " + strings.Join(args, " "))
		body, ok := responses[key]
		if !ok {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestVaultHelperProcess", "--", key)
			cmd.Env = append(os.Environ(),
				"VAULT_TEST_HELPER=1",
				"VAULT_TEST_STDERR=unexpected command: "+key,
				"VAULT_TEST_FAIL=1",
			)
			return cmd
		}
		var stdout, stderr string
		switch {
		case !noisy[key]:
			stdout = body
		case stream == vaultOnStdout:
			stdout = vaultLine + body
		default:
			stderr = vaultLine
			stdout = body
		}
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestVaultHelperProcess", "--", key)
		cmd.Env = append(os.Environ(),
			"VAULT_TEST_HELPER=1",
			"VAULT_TEST_STDOUT="+stdout,
			"VAULT_TEST_STDERR="+stderr,
		)
		return cmd
	}
}

func TestVaultHelperProcess(t *testing.T) {
	if os.Getenv("VAULT_TEST_HELPER") != "1" {
		return
	}
	// stderr first: the Vault line is printed before gh produces any output.
	fmt.Fprint(os.Stderr, os.Getenv("VAULT_TEST_STDERR"))
	fmt.Fprint(os.Stdout, os.Getenv("VAULT_TEST_STDOUT"))
	if os.Getenv("VAULT_TEST_FAIL") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

const (
	vaultRepo = "test/repo"
	vaultSHA  = "deadbeef"

	vaultFindPRCmd      = "gh pr list --head feature --repo test/repo --state open --json number,url,baseRefName"
	vaultPRContentCmd   = "gh pr view 42 --repo test/repo --json title,body"
	vaultPRChecksCmd    = "gh pr checks 42 --repo test/repo --json name,state,bucket,completedAt,link"
	vaultHeadSHACmd     = "gh pr view 42 --repo test/repo --json headRefOid --jq .headRefOid"
	vaultWorkflowRunCmd = "gh api --method GET repos/test/repo/actions/runs -f head_sha=deadbeef -f per_page=100 --paginate --slurp"
	vaultPRStateCmd     = "gh pr view 42 --repo test/repo --json state --jq .state"
	vaultPRBaseCmd      = "gh pr view 42 --repo test/repo --json baseRefName --jq .baseRefName"
	vaultMergeableCmd   = "gh pr view 42 --repo test/repo --json mergeable --jq .mergeable"
	vaultRunListCmd     = "gh run list --branch feature --repo test/repo --status failure --limit 20 --json databaseId,headSha,name,displayTitle,workflowName"
	vaultRunJobsCmd     = "gh run view 101 --repo test/repo --json jobs"
	vaultRunLogCmd      = "gh run view 101 --repo test/repo --log-failed"
)

func vaultReviewThreadsCmd() string {
	return strings.Join([]string{"gh", "api", "graphql", "-f", "query=" + reviewThreadsQuery,
		"-F", "owner=test", "-F", "name=repo", "-F", "number=42"}, " ")
}

func vaultCommitChecksCmd() string {
	return githubCommitChecksCommand("", vaultRepo, vaultSHA)
}

func vaultResponses() map[string]string {
	return map[string]string{
		vaultFindPRCmd:    `[{"number":42,"url":"https://github.com/test/repo/pull/42","baseRefName":"main"}]` + "\n",
		vaultPRContentCmd: `{"title":"fix: vault","body":"body"}` + "\n",
		vaultPRChecksCmd:  `[{"name":"build","state":"SUCCESS","bucket":"pass"}]` + "\n",
		vaultHeadSHACmd:   vaultSHA + "\n",
		vaultCommitChecksCmd(): githubCommitChecksResponse(
			`[{"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"SUCCESS"}]`),
		vaultWorkflowRunCmd: `[{"total_count":0,"workflow_runs":[]}]` + "\n",
		vaultPRStateCmd:     "MERGED\n",
		vaultPRBaseCmd:      "main\n",
		vaultMergeableCmd:   "MERGEABLE\n",
		vaultReviewThreadsCmd(): `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[` +
			`{"isResolved":false,"comments":{"nodes":[{"databaseId":9,"body":"fix","path":"a.go","line":3,` +
			`"url":"https://github.com/test/repo/pull/42#r9","createdAt":"2026-08-27T12:00:00Z","author":{"login":"greptile-apps[bot]"}}]}}` +
			`],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}` + "\n",
		vaultRunListCmd: `[{"databaseId":101,"headSha":"deadbeef","name":"CI","displayTitle":"feature","workflowName":"CI"}]` + "\n",
		vaultRunJobsCmd: `{"jobs":[{"databaseId":7,"name":"unit","conclusion":"failure","steps":[{"name":"Set up job","number":1,"conclusion":"failure"}]}]}` + "\n",
		vaultRunLogCmd:  "unit failed\n",
	}
}

// vaultCase exercises one gh call site with only the commands in noisy
// carrying the Vault line. Splitting noisy per command keeps each case failing
// for its own call site, not for whichever one runs first.
type vaultCase struct {
	name  string
	noisy []string
	run   func(t *testing.T, h *Host)
}

func vaultCases() []vaultCase {
	ctx := context.Background()
	return []vaultCase{
		{"FindPR pr list", []string{vaultFindPRCmd}, func(t *testing.T, h *Host) {
			pr, err := h.FindPR(ctx, "feature", "")
			if err != nil {
				t.Fatalf("FindPR() error = %v", err)
			}
			if pr == nil || pr.Number != "42" {
				t.Fatalf("FindPR() = %+v, want PR 42", pr)
			}
		}},
		{"GetPRContent pr view", []string{vaultPRContentCmd}, func(t *testing.T, h *Host) {
			got, err := h.GetPRContent(ctx, &scm.PR{Number: "42"})
			if err != nil {
				t.Fatalf("GetPRContent() error = %v", err)
			}
			if got.Title != "fix: vault" || got.Body != "body" {
				t.Fatalf("GetPRContent() = %+v", got)
			}
		}},
		{"GetChecks pr checks", []string{vaultPRChecksCmd}, func(t *testing.T, h *Host) {
			checks, err := h.GetChecks(ctx, &scm.PR{Number: "42"})
			if err != nil {
				t.Fatalf("GetChecks() error = %v", err)
			}
			if len(checks) != 1 || checks[0].Name != "build" {
				t.Fatalf("GetChecks() = %+v, want build", checks)
			}
		}},
		{"GetChecks head commit graphql", []string{vaultCommitChecksCmd()}, getChecksAtHead},
		{"GetChecks workflow runs", []string{vaultWorkflowRunCmd}, getChecksAtHead},
		{"GetChecks head sha jq", []string{vaultHeadSHACmd}, getChecksAtHead},
		{"GetPRState jq", []string{vaultPRStateCmd}, func(t *testing.T, h *Host) {
			state, err := h.GetPRState(ctx, &scm.PR{Number: "42"})
			if err != nil {
				t.Fatalf("GetPRState() error = %v", err)
			}
			if state != scm.PRStateMerged {
				t.Fatalf("GetPRState() = %q, want %q", state, scm.PRStateMerged)
			}
		}},
		{"GetPRBaseBranch jq", []string{vaultPRBaseCmd}, func(t *testing.T, h *Host) {
			base, err := h.GetPRBaseBranch(ctx, &scm.PR{Number: "42"})
			if err != nil {
				t.Fatalf("GetPRBaseBranch() error = %v", err)
			}
			if base != "main" {
				t.Fatalf("GetPRBaseBranch() = %q, want main", base)
			}
		}},
		{"GetMergeableState jq", []string{vaultMergeableCmd}, func(t *testing.T, h *Host) {
			state, err := h.GetMergeableState(ctx, &scm.PR{Number: "42"})
			if err != nil {
				t.Fatalf("GetMergeableState() error = %v", err)
			}
			if state != scm.MergeableOK {
				t.Fatalf("GetMergeableState() = %q, want %q", state, scm.MergeableOK)
			}
		}},
		{"GetReviewComments graphql", []string{vaultReviewThreadsCmd()}, func(t *testing.T, h *Host) {
			comments, err := h.GetReviewComments(ctx, &scm.PR{Number: "42"})
			if err != nil {
				t.Fatalf("GetReviewComments() error = %v", err)
			}
			if len(comments) != 1 || comments[0].ID != "9" {
				t.Fatalf("GetReviewComments() = %+v, want comment 9", comments)
			}
		}},
		{"FetchFailedCheckLogs run list", []string{vaultRunListCmd}, func(t *testing.T, h *Host) {
			logs, err := h.FetchFailedCheckLogs(ctx, &scm.PR{Number: "42"}, "feature", "", []string{"ci"})
			if err != nil {
				t.Fatalf("FetchFailedCheckLogs() error = %v", err)
			}
			if logs != "unit failed" {
				t.Fatalf("FetchFailedCheckLogs() = %q, want the failed log", logs)
			}
		}},
		{"FetchFailedCheckLogs run view jobs", []string{vaultRunJobsCmd}, func(t *testing.T, h *Host) {
			logs, err := h.FetchFailedCheckLogs(ctx, &scm.PR{Number: "42"}, "feature", "", []string{"unit"})
			if err != nil {
				t.Fatalf("FetchFailedCheckLogs() error = %v", err)
			}
			if logs != "unit failed" {
				t.Fatalf("FetchFailedCheckLogs() = %q, want the failed log matched through the job name", logs)
			}
		}},
		{"PreRunFailures run view jobs", []string{vaultRunJobsCmd}, func(t *testing.T, h *Host) {
			infra, err := h.PreRunFailures(ctx, []scm.Check{{
				Name: "unit", Bucket: scm.CheckBucketFail, State: "FAILURE",
				Link: "https://github.com/test/repo/actions/runs/101/job/7",
			}})
			if err != nil {
				t.Fatalf("PreRunFailures() error = %v", err)
			}
			if len(infra) != 1 || !infra[0] {
				t.Fatalf("PreRunFailures() = %v, want the setup failure flagged", infra)
			}
		}},
	}
}

func getChecksAtHead(t *testing.T, h *Host) {
	t.Helper()
	checks, err := h.GetChecks(context.Background(), &scm.PR{Number: "42", HeadSHA: vaultSHA})
	if err != nil {
		t.Fatalf("GetChecks() error = %v", err)
	}
	if len(checks) != 1 || checks[0].Name != "build" {
		t.Fatalf("GetChecks() = %+v, want build", checks)
	}
}

func TestGHCallSitesToleratePrefixedVaultLine(t *testing.T) {
	t.Parallel()

	for _, stream := range []vaultStream{vaultOnStdout, vaultOnStderr} {
		for _, tc := range vaultCases() {
			t.Run(string(stream)+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				noisy := map[string]bool{}
				for _, key := range tc.noisy {
					noisy[key] = true
				}
				host := New(vaultCmdFactory(stream, noisy, vaultResponses()), nil, "", vaultRepo)
				tc.run(t, host)
			})
		}
	}
}

// Output that holds the Vault line and no JSON at all must say what gh printed,
// not "invalid character 'a' looking for beginning of value".
func TestFindPRNoJSONNamesWhatGHPrinted(t *testing.T) {
	t.Parallel()

	responses := vaultResponses()
	responses[vaultFindPRCmd] = ""
	host := New(vaultCmdFactory(vaultOnStdout, map[string]bool{vaultFindPRCmd: true}, responses), nil, "", vaultRepo)

	pr, err := host.FindPR(context.Background(), "feature", "")
	if err == nil {
		t.Fatalf("FindPR() = %+v, error = nil, want an error", pr)
	}
	for _, want := range []string{"parse gh pr list JSON", "no JSON", "automic vault: human approval required"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("FindPR() error = %q, want it to contain %q", err, want)
		}
	}
}
