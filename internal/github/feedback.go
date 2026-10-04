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
// order, count as feedback, and whether a change request still stands.
//
// Only authors with write access (OWNER, MEMBER, COLLABORATOR) or listed in
// bots count, and never the Operator, whose account Claude itself comments
// as. A submitted review counts when it comments or requests changes; a
// comment counts when it says something outside quoted text and code
// fences. A change request stands while it is the latest verdict (approval
// or change request) of a counted author. GitHub turns a dismissed review
// into a review comment, in place, so it carries no verdict: dismissing an
// approval over a change request leaves the change request standing.
func Triage(operator string, bots []string, reviews []Review, comments []Comment) (feedback []Feedback, changesRequested bool) {
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
		case "APPROVED":
			verdicts[strings.ToLower(r.Author)] = state
		}
	}
	for _, c := range comments {
		if allowed(c.Author, c.AuthorAssociation) && saysSomething(c.Body) {
			feedback = append(feedback, Feedback{ID: c.ID, At: c.CreatedAt})
		}
	}

	for _, v := range verdicts {
		if v == "CHANGES_REQUESTED" {
			return feedback, true
		}
	}
	return feedback, false
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
