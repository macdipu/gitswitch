package domain

import (
	"strings"
	"time"
)

// PullRequest is an open pull request as the inbox shows it.
type PullRequest struct {
	Repo      string `json:"repo"` // owner/name
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
	Chips     []Chip `json:"chips"`
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
		chips = append(chips, Chip{Kind: "draft", Text: "Draft", Hint: "Mark it ready for review on GitHub when it is"})
	}
	if p.Mergeable == "CONFLICTING" {
		chips = append(chips, Chip{Kind: "conflict", Text: "Conflicts", Hint: "Its branch clashes with the base branch: update it and fix the clashing files", Bad: true})
	}
	if failing {
		chips = append(chips, Chip{Kind: "failing", Text: "Checks failing", Hint: "Open it to see which check failed", Bad: true})
	} else if p.Checks == "PENDING" || p.Checks == "EXPECTED" {
		chips = append(chips, Chip{Kind: "running", Text: "Checks running"})
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
