package github

import (
	"strings"
	"time"
)

// Review is a review on a pull request.
type Review struct {
	ID                string
	Author            string
	AuthorAssociation string
	Body              string
	// State is "COMMENTED", "CHANGES_REQUESTED", "APPROVED", "DISMISSED"
	// or "PENDING".
	State       string
	SubmittedAt time.Time
}

// Comment is a standalone comment on a pull request's conversation.
type Comment struct {
	ID                string
	Author            string
	AuthorAssociation string
	Body              string
	CreatedAt         time.Time
}

// Feedback is a review or comment that counts as feedback on a pull request.
type Feedback struct {
	ID string
	// Review is set for a submitted review, unset for a standalone comment.
	Review bool
	At     time.Time
}

// Triage decides which reviews and comments, in GitHub's chronological
// order, count as feedback, and whether the pull request is approved.
//
// Only authors with write access (OWNER, MEMBER, COLLABORATOR) or listed in
// bots count, and never the Operator, whose account Claude itself comments
// as. A submitted review counts when it comments or requests changes; a
// comment counts when it says something outside quoted text and code
// fences. The PR is approved when, taking each counted author's latest
// approval, change request or dismissal, at least one approves and none
// requests changes.
func Triage(operator string, bots []string, reviews []Review, comments []Comment) (feedback []Feedback, approved bool) {
	allowed := func(author, association string) bool {
		if strings.EqualFold(author, operator) {
			return false
		}
		switch strings.ToUpper(association) {
		case "OWNER", "MEMBER", "COLLABORATOR":
			return true
		}
		for _, b := range bots {
			if botLogin(b) == botLogin(author) {
				return true
			}
		}
		return false
	}

	verdicts := map[string]string{}
	for _, r := range reviews {
		if !allowed(r.Author, r.AuthorAssociation) {
			continue
		}
		switch state := strings.ToUpper(r.State); state {
		case "COMMENTED", "CHANGES_REQUESTED":
			feedback = append(feedback, Feedback{ID: r.ID, Review: true, At: r.SubmittedAt})
			if state == "CHANGES_REQUESTED" {
				verdicts[strings.ToLower(r.Author)] = state
			}
		case "APPROVED", "DISMISSED":
			verdicts[strings.ToLower(r.Author)] = state
		}
	}
	for _, c := range comments {
		if allowed(c.Author, c.AuthorAssociation) && saysSomething(c.Body) {
			feedback = append(feedback, Feedback{ID: c.ID, At: c.CreatedAt})
		}
	}

	for _, v := range verdicts {
		switch v {
		case "CHANGES_REQUESTED":
			return feedback, false
		case "APPROVED":
			approved = true
		}
	}
	return feedback, approved
}

// botLogin normalises a bot's login: GitHub's REST API names a bot
// "name[bot]", its GraphQL API (and so gh) "name", and gh's text output
// "app/name".
func botLogin(login string) string {
	login = strings.ToLower(login)
	login = strings.TrimPrefix(login, "app/")
	return strings.TrimSuffix(login, "[bot]")
}

// saysSomething reports whether body has text outside quoted lines and code
// fences.
func saysSomething(body string) bool {
	fence := ""
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(line, fence) {
				fence = ""
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "```"):
			fence = "```"
		case strings.HasPrefix(line, "~~~"):
			fence = "~~~"
		case line == "" || strings.HasPrefix(line, ">"):
		default:
			return true
		}
	}
	return false
}
