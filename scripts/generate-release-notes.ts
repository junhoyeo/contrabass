#!/usr/bin/env bun
/**
 * Appends contributor attribution to an existing GitHub release created by GoReleaser.
 *
 * Usage: bun scripts/generate-release-notes.ts <tag>
 * Env:   GITHUB_REPOSITORY (default: junhoyeo/contrabass)
 */
export {};

import { execFileSync } from "node:child_process";
import { writeFileSync, unlinkSync } from "node:fs";

const REPO = process.env.GITHUB_REPOSITORY || "junhoyeo/contrabass";

interface Commit {
  hash: string;
  message: string;
  authorName: string;
  authorEmail: string;
}

interface PRInfo {
  number: number;
  title: string;
  authorLogin: string;
}

interface ContributorEntry {
  author: string;
  message: string;
  prNumber?: number;
}

interface NewContributor {
  username: string;
  firstPrNumber: number;
}

function run(command: string, args: string[], allowFailure = false): string {
  try {
    return execFileSync(command, args, {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  } catch (error) {
    if (allowFailure) return "";
    if (error instanceof Error) {
      throw new Error(`${command} ${args.join(" ")} failed: ${error.message}`);
    }
    throw error;
  }
}

function runJson<T>(command: string, args: string[], allowFailure = false): T | null {
  const output = run(command, args, allowFailure);
  if (!output) return null;
  try {
    return JSON.parse(output) as T;
  } catch {
    return null;
  }
}

function getPreviousTag(currentTag: string): string | null {
  const tag = run("git", ["describe", "--tags", "--abbrev=0", `${currentTag}^`], true);
  return tag || null;
}

function getTagDate(tag: string): string {
  return run("git", ["log", "-1", "--format=%cI", tag]);
}

function getCommitsBetween(fromTag: string, toTag: string): Commit[] {
  const output = run("git", [
    "log",
    `${fromTag}..${toTag}`,
    "--format=%H%x1f%s%x1f%an%x1f%ae",
    "--no-merges",
  ]);
  if (!output) return [];
  return output
    .split("\n")
    .filter((line) => line.trim())
    .map((line) => {
      const [hash = "", message = "", authorName = "", authorEmail = ""] =
        line.split("\x1f");
      return { hash, message, authorName, authorEmail };
    })
    .filter(
      (entry) =>
        entry.hash &&
        !entry.message.startsWith("chore: bump version") &&
        !entry.message.startsWith("Merge"),
    );
}

function resolveGitHubUsername(email: string, fallbackName: string): string {
  if (email.includes("@users.noreply.github.com")) {
    const match = email.match(
      /(?:\d+\+)?([^@]+)@users\.noreply\.github\.com/,
    );
    if (match?.[1]) return `@${match[1]}`;
  }

  const search = runJson<{ items?: Array<{ login?: string }> }>(
    "gh",
    ["api", `/search/users?q=${encodeURIComponent(email)}+in:email`],
    true,
  );
  const login = search?.items?.[0]?.login;
  return login ? `@${login}` : fallbackName;
}

function findAssociatedPR(commitHash: string): PRInfo | null {
  const result = runJson<
    Array<{
      number: number;
      title: string;
      state: string;
      merged_at?: string | null;
      user?: { login?: string };
    }>
  >("gh", ["api", `repos/${REPO}/commits/${commitHash}/pulls`], true);
  if (!result?.length) return null;

  const pr =
    result.find((p) => p.merged_at != null) ??
    result.find((p) => p.state === "closed") ??
    result[0];
  if (!pr?.number || !pr.user?.login) return null;

  return { number: pr.number, title: pr.title, authorLogin: pr.user.login };
}

function isFirstContributionAfter(
  login: string,
  thresholdDate: string,
): NewContributor | null {
  const result = runJson<Array<{ number: number; mergedAt: string }>>(
    "gh",
    [
      "pr",
      "list",
      "--repo",
      REPO,
      "--state",
      "merged",
      "--author",
      login,
      "--json",
      "number,mergedAt",
      "--limit",
      "200",
    ],
    true,
  );
  if (!result?.length) return null;
  const oldest = [...result].sort(
    (a, b) => new Date(a.mergedAt).getTime() - new Date(b.mergedAt).getTime(),
  )[0];
  return new Date(oldest.mergedAt) > new Date(thresholdDate)
    ? { username: `@${login}`, firstPrNumber: oldest.number }
    : null;
}

function getExistingReleaseBody(tag: string): string {
  return run(
    "gh",
    ["release", "view", tag, "--repo", REPO, "--json", "body", "-q", ".body"],
  );
}

function updateReleaseBody(tag: string, body: string): void {
  const tmpFile = `/tmp/contrabass-release-${Date.now()}.md`;
  writeFileSync(tmpFile, body);
  try {
    run("gh", ["release", "edit", tag, "--repo", REPO, "--notes-file", tmpFile]);
  } finally {
    unlinkSync(tmpFile);
  }
}

function main(): void {
  const tag = process.argv[2];
  if (!tag) {
    console.error("Usage: bun scripts/generate-release-notes.ts <tag>");
    process.exit(1);
  }

  const prevTag = getPreviousTag(tag);
  if (!prevTag) {
    console.error("No previous tag found, skipping contributor attribution.");
    process.exit(0);
  }

  const prevTagDate = getTagDate(prevTag);
  const commits = getCommitsBetween(prevTag, tag);
  const contributors: ContributorEntry[] = [];
  const candidateLogins = new Set<string>();
  const seenPRs = new Set<number>();

  for (const commit of commits) {
    const prInfo = findAssociatedPR(commit.hash);

    if (prInfo?.number && seenPRs.has(prInfo.number)) {
      continue;
    }
    if (prInfo?.number) {
      seenPRs.add(prInfo.number);
    }

    const author = prInfo
      ? `@${prInfo.authorLogin}`
      : resolveGitHubUsername(commit.authorEmail, commit.authorName);

    contributors.push({
      author,
      message: prInfo?.title || commit.message,
      prNumber: prInfo?.number,
    });

    if (prInfo?.authorLogin) {
      candidateLogins.add(prInfo.authorLogin);
    }
  }

  if (contributors.length === 0) {
    console.log("No contributors to attribute.");
    return;
  }

  const newContributors = Array.from(candidateLogins)
    .map((login) => isFirstContributionAfter(login, prevTagDate))
    .filter((item): item is NewContributor => Boolean(item));

  const appendLines: string[] = ["", "---", "", "## Contributors"];

  for (const entry of contributors) {
    const prLink = entry.prNumber
      ? ` in https://github.com/${REPO}/pull/${entry.prNumber}`
      : "";
    appendLines.push(`* ${entry.message} by ${entry.author}${prLink}`);
  }

  if (newContributors.length > 0) {
    appendLines.push("", "## New Contributors");
    for (const c of newContributors) {
      appendLines.push(
        `* ${c.username} made their first contribution in https://github.com/${REPO}/pull/${c.firstPrNumber}`,
      );
    }
  }

  appendLines.push(
    "",
    `**Full Changelog**: https://github.com/${REPO}/compare/${prevTag}...${tag}`,
  );

  const existingBody = getExistingReleaseBody(tag);
  if (existingBody.includes("## Contributors")) {
    console.log(`Release ${tag} already contains contributor attribution, skipping.`);
    return;
  }
  const updatedBody = existingBody + appendLines.join("\n") + "\n";

  updateReleaseBody(tag, updatedBody);
  console.log(`Updated release ${tag} with contributor attribution.`);
}

main();
