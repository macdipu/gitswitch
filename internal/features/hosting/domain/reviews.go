package domain

import (
	"strconv"
	"strings"
	"time"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
)

// PullRequest is an open pull request (a merge request on GitLab) as the
// inbox shows it.
type PullRequest struct {
	// Kind is the kind of host it is on, and Mark what goes before its
	// number there (# or !); both are filled in by the use case.
	Kind      Kind   `json:"kind"`
	Mark      string `json:"mark"`
	Repo      string `json:"repo"` // owner/name (group/subgroup/name on GitLab)
	Number    int    `json:"number"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Author    string `json:"author"`
	Draft     bool   `json:"draft"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	// ReviewDecision is GitHub's: APPROVED, CHANGES_REQUESTED,
	// REVIEW_REQUIRED, or "" when the repo needs no review.
	ReviewDecision string `json:"reviewDecision"`
	// Checks is the state of the last commit's checks: SUCCESS, FAILURE,
	// ERROR, PENDING, EXPECTED, or "" with none.
	Checks string `json:"checks"`
	// Mergeable is MERGEABLE, CONFLICTING or UNKNOWN (still computing).
	Mergeable string `json:"mergeable"`
	// ReadyToMerge is a host that says so itself (GitLab's "mergeable"):
	// no conflicts, checks passed and approvals met, whoever approved.
	ReadyToMerge bool   `json:"readyToMerge"`
	Chips        []Chip `json:"chips"`
	// Approvals counts reviewers whose latest opinion is an approval; read
	// for a branch's pull request only (BranchPR), 0 in the inbox.
	Approvals int `json:"approvals"`
}

// Chip is one thing to know about a pull request, in plain words.
type Chip struct {
	Kind string `json:"kind"` // draft, conflict, failing, running, changes, approved, ready, waiting
	Text string `json:"text"`
	Hint string `json:"hint"` // what to do about it
	Bad  bool   `json:"bad"`  // needs your attention
}

// StatusChips says what stands out about a pull request, the most
// pressing first.
func StatusChips(p PullRequest) []Chip {
	chips := []Chip{}
	failing := p.Checks == "FAILURE" || p.Checks == "ERROR"
	if p.Draft {
		chips = append(chips, Chip{Kind: "draft", Text: "Draft", Hint: "Mark it ready for review when it is"})
	}
	if p.Mergeable == "CONFLICTING" {
		chips = append(chips, Chip{Kind: "conflict", Text: "Conflicts", Hint: "Its branch clashes with the base branch: update it and fix the clashing files", Bad: true})
	}
	if failing {
		chips = append(chips, Chip{Kind: "failing", Text: "Checks failing", Hint: "Open it to see which check failed", Bad: true})
	} else if p.Checks == "PENDING" || p.Checks == "EXPECTED" {
		chips = append(chips, Chip{Kind: "running", Text: "Checks running"})
	}
	if p.ReadyToMerge && !p.Draft && p.ReviewDecision != "CHANGES_REQUESTED" {
		chips = append(chips, Chip{Kind: "ready", Text: "Ready to merge", Hint: "No conflicts, checks passing and approvals met"})
		return chips
	}
	switch p.ReviewDecision {
	case "CHANGES_REQUESTED":
		chips = append(chips, Chip{Kind: "changes", Text: "Changes requested", Hint: "A reviewer asked for changes: push them, then ask again", Bad: true})
	case "APPROVED":
		if !p.Draft && p.Mergeable != "CONFLICTING" && !failing && p.Checks != "PENDING" && p.Checks != "EXPECTED" {
			chips = append(chips, Chip{Kind: "ready", Text: "Approved, ready to merge", Hint: "Approved, no conflicts and checks passing"})
		} else {
			chips = append(chips, Chip{Kind: "approved", Text: "Approved"})
		}
	case "REVIEW_REQUIRED":
		if !p.Draft {
			chips = append(chips, Chip{Kind: "waiting", Text: "Waiting for review"})
		}
	}
	return chips
}

// Inbox is one signed-in account's pull requests.
type Inbox struct {
	Account  string        `json:"account"`
	Host     string        `json:"host"`
	Login    string        `json:"login"`
	Fetched  time.Time     `json:"fetched"` // zero: never read
	Stale    bool          `json:"stale"`
	Error    string        `json:"error"`
	ToReview []PullRequest `json:"toReview"` // waiting for this account's review
	Mine     []PullRequest `json:"mine"`     // opened by this account
	Reviewed []PullRequest `json:"reviewed"` // open, reviewed by this account lately
}

// ReviewsStale is how long an inbox is shown before it is read again.
const ReviewsStale = 5 * time.Minute

// InboxQueries are the searches for an account's inbox, by its login:
// review requested from it (or a team it is on), its own open pull
// requests, and open ones it reviewed, newest activity first.
func InboxQueries(login string) (toReview, mine, reviewed string) {
	const open = "is:pr is:open archived:false sort:updated-desc "
	return open + "review-requested:" + login,
		open + "author:" + login,
		open + "reviewed-by:" + login + " -author:" + login
}

// GraphQLURL is the GraphQL endpoint for a REST API address:
// api.github.com/graphql, or <host>/api/graphql on GitHub Enterprise.
func GraphQLURL(restBase string) string {
	if b, ok := strings.CutSuffix(strings.TrimSuffix(restBase, "/"), "/api/v3"); ok {
		return b + "/api/graphql"
	}
	return strings.TrimSuffix(restBase, "/") + "/graphql"
}

// BranchPR is what the History toolbar says about the current branch's
// open pull request.
type BranchPR struct {
	Found   bool   `json:"found"`   // the branch has an open pull request
	Account string `json:"account"` // the account that read it
	// Kind is the kind of host the account is on: it names what is shown
	// (pull request or merge request); "" when no account decided.
	Kind   Kind   `json:"kind"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	// Reason is why nothing is shown when Found is false and something
	// stands in the way (no account signed in, an unreadable answer); ""
	// when the branch simply has no pull request.
	Reason string      `json:"reason"`
	PR     PullRequest `json:"pr"`
	// Summary is "PR #42 · 1 approval · checks passing".
	Summary string `json:"summary"`
}

// BranchSummary is a pull request in one line for a toolbar.
func BranchSummary(p PullRequest) string {
	parts := []string{p.Kind.Short() + " " + p.Kind.Mark() + strconv.Itoa(p.Number)}
	if p.Draft {
		parts = append(parts, "draft")
	}
	switch {
	case p.ReviewDecision == "CHANGES_REQUESTED":
		parts = append(parts, "changes requested")
	case p.Approvals == 1:
		parts = append(parts, "1 approval")
	case p.Approvals > 1:
		parts = append(parts, strconv.Itoa(p.Approvals)+" approvals")
	case p.ReviewDecision == "REVIEW_REQUIRED":
		parts = append(parts, "waiting for review")
	default:
		parts = append(parts, "no approvals")
	}
	switch p.Checks {
	case "SUCCESS":
		parts = append(parts, "checks passing")
	case "FAILURE", "ERROR":
		parts = append(parts, "checks failing")
	case "PENDING", "EXPECTED":
		parts = append(parts, "checks running")
	}
	if p.Mergeable == "CONFLICTING" {
		parts = append(parts, "conflicts")
	}
	return strings.Join(parts, " · ")
}

// RepoOf is owner/name for a GitHub remote URL, "" for anything else.
func RepoOf(remoteURL string) string { return RepoPath(KindGitHub, remoteURL) }

// RepoPath is the repo's path on its host for a remote URL: owner/name on
// GitHub, and on GitLab group/name or group/subgroup/name. "" for a URL
// that is not one.
func RepoPath(kind Kind, remoteURL string) string {
	_, p := remote.Parse(remoteURL)
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	n := strings.Count(p, "/")
	if (kind == KindGitLab && n < 1) || (kind != KindGitLab && n != 1) {
		return ""
	}
	return p
}
