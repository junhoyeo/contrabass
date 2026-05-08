# Linear tracker — `BlockedBy` from `inverseRelations`

## ADDED Requirements

### Requirement: Linear tracker SHALL populate `Issue.BlockedBy` from `inverseRelations` of type `blocks`

`LinearClient.FetchIssues` SHALL request each issue's
`inverseRelations` field with a `type` filter and the related issue's
`identifier`. `normalizeIssue` SHALL produce a non-nil `[]string`
slice in `Issue.BlockedBy` whose elements are the `identifier`s of the
issues whose relation to the current issue has `type == "blocks"` —
i.e. the issues that *block* the current one. Issues with no
incoming `blocks` relations SHALL keep `BlockedBy` as an empty (not
nil) slice so the existing JSON-stable contract is preserved.

#### Scenario: Issue with one inverse blocks relation

- GIVEN a Linear issue `ZII-50` whose `inverseRelations` payload
  contains `[{ "type": "blocks", "issue": { "identifier": "ZII-49" } }]`
- WHEN `FetchIssues` returns the issue
- THEN `issue.BlockedBy` equals `["ZII-49"]`.

#### Scenario: Issue with multiple inverse blocks relations

- GIVEN an issue whose `inverseRelations` contains two `type: blocks`
  entries pointing at `ZII-49` and `ZII-50` and one `type: related`
  entry pointing at `ZII-44`
- WHEN `FetchIssues` returns the issue
- THEN `issue.BlockedBy` is a slice that contains `ZII-49` and `ZII-50`
  in deterministic (input) order, and does NOT contain `ZII-44`.

#### Scenario: Issue with no inverse relations

- GIVEN an issue whose `inverseRelations.nodes` is the empty list
- WHEN `FetchIssues` returns the issue
- THEN `issue.BlockedBy` is `[]string{}` (non-nil, length 0).

#### Scenario: Outgoing `relations` of type `blocks` are ignored for `BlockedBy`

- GIVEN an issue whose **outgoing** `relations` field contains
  `[{ "type": "blocks", "relatedIssue": { "identifier": "ZII-99" } }]`
  (i.e. this issue blocks `ZII-99`) but whose `inverseRelations` is
  empty
- WHEN `FetchIssues` returns the issue
- THEN `issue.BlockedBy` is `[]string{}`.
  Rationale: `BlockedBy` lists who blocks me, not who I block.

#### Scenario: Missing identifier on a related issue is skipped

- GIVEN an `inverseRelations` node whose `issue.identifier` is missing
  or empty
- WHEN `normalizeIssue` parses the response
- THEN that node SHALL be silently skipped; well-formed nodes from the
  same response SHALL still appear in `BlockedBy`.
