#!/usr/bin/env python3

import argparse
import json
import os
import sys
import urllib.error
import urllib.request


ENDPOINT = "https://api.linear.app/graphql"


def gql(api_key, query, variables):
    body = json.dumps({"query": query, "variables": variables}).encode()
    req = urllib.request.Request(
        ENDPOINT,
        data=body,
        headers={"Authorization": api_key, "Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            payload = json.loads(resp.read().decode())
    except urllib.error.HTTPError as err:
        message = err.read().decode()
        raise RuntimeError(f"Linear HTTP {err.code}: {message}") from err

    if "errors" in payload and payload["errors"]:
        raise RuntimeError(f"Linear GraphQL error: {payload['errors']}")
    return payload.get("data", {})


def get_team(api_key, team_key):
    query = "query { teams(first: 100) { nodes { id key name } } }"
    nodes = gql(api_key, query, {}).get("teams", {}).get("nodes", [])
    for team in nodes:
        if team.get("key") == team_key:
            return team
    raise RuntimeError(f"Team key not found: {team_key}")


def get_or_create_project(api_key, team_id, project_name):
    list_query = "query { projects(first: 100) { nodes { id name slugId url } } }"
    projects = gql(api_key, list_query, {}).get("projects", {}).get("nodes", [])
    for project in projects:
        if project.get("name") == project_name:
            return project

    create_query = """
    mutation($name: String!, $teamIds: [String!]!) {
      projectCreate(input: {name: $name, teamIds: $teamIds}) {
        success
        project { id name slugId url }
      }
    }
    """
    created = gql(api_key, create_query, {"name": project_name, "teamIds": [team_id]})
    project = created.get("projectCreate", {}).get("project")
    if not project:
        raise RuntimeError("Failed to create project")
    return project


def create_issue(api_key, team_id, project_id, title, description):
    mutation = """
    mutation($teamId: String!, $projectId: String!, $title: String!, $description: String!) {
      issueCreate(input: {
        teamId: $teamId,
        projectId: $projectId,
        title: $title,
        description: $description
      }) {
        success
        issue { id identifier title url }
      }
    }
    """
    out = gql(
        api_key,
        mutation,
        {
            "teamId": team_id,
            "projectId": project_id,
            "title": title,
            "description": description,
        },
    )
    issue = out.get("issueCreate", {}).get("issue")
    if not issue:
        raise RuntimeError(f"Failed to create issue: {title}")
    return issue


def main():
    parser = argparse.ArgumentParser(
        description="Seed Linear with parallel demo tickets for Contrabass"
    )
    parser.add_argument("--team-key", default="ODO", help="Linear team key")
    parser.add_argument(
        "--project-name",
        default="Contrabass Symphony Demo",
        help="Project name to use/create",
    )
    parser.add_argument(
        "--count", type=int, default=6, help="Number of issues to create"
    )
    parser.add_argument("--tag", default="DEMO", help="Title tag prefix")
    args = parser.parse_args()

    api_key = os.getenv("LINEAR_API_KEY", "").strip()
    if not api_key:
        print("LINEAR_API_KEY is required", file=sys.stderr)
        sys.exit(1)

    team = get_team(api_key, args.team_key)
    project = get_or_create_project(api_key, team["id"], args.project_name)

    templates = [
        "Build robust retry/backoff handling for tracker fetch with deterministic jitter and tests.",
        "Implement config hot-reload validation with rollback on malformed YAML and snapshot coverage.",
        "Refactor TUI viewport rendering to handle large backoff queues and ensure smooth scroll behavior.",
        "Add orchestrator reconciliation for orphaned runs and validate graceful shutdown race conditions.",
        "Improve agent event aggregation metrics (tokens, runtime, throughput) with consistency checks.",
        "Harden workspace lifecycle hooks with failure isolation and structured diagnostic logging.",
        "Introduce integration test for multi-issue parallel scheduling and claim/release correctness.",
        "Add protocol error resilience for malformed JSONL responses and recovery without process crash.",
    ]

    created = []
    for i in range(args.count):
        template = templates[i % len(templates)]
        title = f"[{args.tag}] Parallel demo task {i + 1}"
        desc = (
            "## Goal\n"
            + template
            + "\n\n"
            + "## Acceptance Criteria\n"
            + "- Include code changes\n"
            + "- Include tests\n"
            + "- Maintain backward compatibility\n"
            + "- Document assumptions and risks"
        )
        issue = create_issue(api_key, team["id"], project["id"], title, desc)
        created.append(issue)

    print(f"Project: {project['name']}")
    print(f"Project URL: {project['url']}")
    print(f"Project Slug: {project['slugId']}")
    print("Created issues:")
    for issue in created:
        print(f"- {issue['identifier']} {issue['title']} {issue['url']}")


if __name__ == "__main__":
    main()
