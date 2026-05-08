package tracker

import (
	"context"
	"fmt"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

const issueDetailQuery = `query IssueDetail($issueId: String!) {
	issue(id: $issueId) {
		id
		identifier
		title
		description
		priority
		state { name type }
		url
		labels { nodes { name } }
		createdAt
		updatedAt
		inverseRelations {
			nodes {
				type
				issue { id identifier title url state { name type } }
				relatedIssue { id identifier title url state { name type } }
			}
		}
		relations {
			nodes {
				type
				issue { id identifier title url state { name type } }
				relatedIssue { id identifier title url state { name type } }
			}
		}
		assignee { id name displayName }
		creator { id name displayName }
		team { id key name }
		project { id name url }
		cycle { id name number startsAt endsAt }
		estimate
		dueDate
	}
}`

// FetchIssueDetail retrieves rich Linear issue metadata without changing the
// lean FetchIssues polling query used by the orchestrator.
func (c *LinearClient) FetchIssueDetail(ctx context.Context, issueID string) (IssueDetail, error) {
	data, err := c.doGraphQL(ctx, issueDetailQuery, map[string]interface{}{"issueId": issueID})
	if err != nil {
		return IssueDetail{}, err
	}

	rawIssue, _ := data["issue"].(map[string]interface{})
	if rawIssue == nil {
		return IssueDetail{}, fmt.Errorf("issue %s not found", issueID)
	}

	issue := normalizeIssue(rawIssue)
	return IssueDetail{
		Issue: issue,
		Linear: &LinearIssueDetail{
			Assignee:  linearUserSummary(rawIssue["assignee"]),
			Creator:   linearUserSummary(rawIssue["creator"]),
			Team:      linearNamedRef(rawIssue["team"]),
			Project:   linearNamedRef(rawIssue["project"]),
			Cycle:     linearCycleSummary(rawIssue["cycle"]),
			Estimate:  linearOptionalFloat(rawIssue["estimate"]),
			DueDate:   getString(rawIssue, "dueDate"),
			Relations: linearRelationSummaries(rawIssue),
			FetchedAt: time.Now().UTC(),
		},
	}, nil
}

var _ IssueDetailProvider = (*LinearClient)(nil)

func linearUserSummary(value interface{}) *LinearUserSummary {
	node, _ := value.(map[string]interface{})
	if node == nil {
		return nil
	}
	id := getString(node, "id")
	if id == "" {
		return nil
	}
	return &LinearUserSummary{
		ID:          id,
		Name:        getString(node, "name"),
		DisplayName: getString(node, "displayName"),
	}
}

func linearNamedRef(value interface{}) *LinearNamedRef {
	node, _ := value.(map[string]interface{})
	if node == nil {
		return nil
	}
	id := getString(node, "id")
	if id == "" {
		return nil
	}
	return &LinearNamedRef{
		ID:   id,
		Key:  getString(node, "key"),
		Name: getString(node, "name"),
		URL:  getString(node, "url"),
	}
}

func linearCycleSummary(value interface{}) *LinearCycleSummary {
	node, _ := value.(map[string]interface{})
	if node == nil {
		return nil
	}
	id := getString(node, "id")
	if id == "" {
		return nil
	}
	return &LinearCycleSummary{
		ID:       id,
		Name:     getString(node, "name"),
		Number:   getInt(node, "number"),
		StartsAt: getString(node, "startsAt"),
		EndsAt:   getString(node, "endsAt"),
	}
}

func linearOptionalFloat(value interface{}) *float64 {
	switch typed := value.(type) {
	case float64:
		return &typed
	case int:
		asFloat := float64(typed)
		return &asFloat
	default:
		return nil
	}
}

func linearRelationSummaries(rawIssue map[string]interface{}) []LinearRelationSummary {
	out := make([]LinearRelationSummary, 0)
	out = append(out, linearRelationSummariesFrom(rawIssue["relations"], "outbound")...)
	out = append(out, linearRelationSummariesFrom(rawIssue["inverseRelations"], "inverse")...)
	if out == nil {
		return []LinearRelationSummary{}
	}
	return out
}

func linearRelationSummariesFrom(value interface{}, direction string) []LinearRelationSummary {
	container, _ := value.(map[string]interface{})
	if container == nil {
		return nil
	}
	nodes, _ := container["nodes"].([]interface{})
	if len(nodes) == 0 {
		return nil
	}

	summaries := make([]LinearRelationSummary, 0, len(nodes))
	for _, nodeValue := range nodes {
		node, _ := nodeValue.(map[string]interface{})
		if node == nil {
			continue
		}
		related := linearRelatedIssueSummary(node["relatedIssue"])
		if related.ID == "" {
			related = linearRelatedIssueSummary(node["issue"])
		}
		if related.ID == "" {
			continue
		}
		summaries = append(summaries, LinearRelationSummary{
			Type:      getString(node, "type"),
			Direction: direction,
			Issue:     related,
		})
	}
	return summaries
}

func linearRelatedIssueSummary(value interface{}) LinearRelatedIssueSummary {
	node, _ := value.(map[string]interface{})
	if node == nil {
		return LinearRelatedIssueSummary{}
	}

	stateName, stateType := "", ""
	if state, _ := node["state"].(map[string]interface{}); state != nil {
		stateName = getString(state, "name")
		stateType = getString(state, "type")
	}

	return LinearRelatedIssueSummary{
		ID:         getString(node, "id"),
		Identifier: getString(node, "identifier"),
		Title:      getString(node, "title"),
		URL:        getString(node, "url"),
		State:      stateName,
		StateType:  stateType,
	}
}

func detailIssueFromSnapshot(issue types.Issue) IssueDetail {
	return IssueDetail{Issue: issue}
}
