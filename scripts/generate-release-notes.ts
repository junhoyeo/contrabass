#!/usr/bin/env bun
/**
 * Usage: bun scripts/generate-release-notes.ts <version>
 * Env:   GITHUB_REPOSITORY (default: junhoyeo/contrabass)
 */
export {};

import { execFileSync } from "node:child_process";

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

interface ChangeEntry {
  hash: string;
  message: string;
  author: string;
  prNumber?: number;
  group: string;
  groupOrder: number;
}

interface ContributorInfo {
  username: string;
  firstPrNumber: number;
}

const COMMIT_GROUPS: Array<{ title: string; prefix: string; order: number }> = [
  { title: "🚀 Features", prefix: "feat", order: 0 },
  { title: "🐛 Bug Fixes", prefix: "fix", order: 1 },
  { title: "⚡ Performance", prefix: "perf", order: 2 },
  { title: "♻️ Refactoring", prefix: "refactor", order: 3 },
  { title: "📝 Documentation", prefix: "docs", order: 4 },
  { title: "🧪 Tests", prefix: "test", order: 5 },
  { title: "🔧 Chore", prefix: "chore", order: 6 },
];

function classifyCommit(message: string): { group: string; order: number } {
  for (const g of COMMIT_GROUPS) {
    const re = new RegExp(`^${g.prefix}(\\([^)]+\\))?!?:`);
    if (re.test(message)) return { group: g.title, order: g.order };
  }
  return { group: "🔧 Other", order: 999 };
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

function getPreviousTag(): string | null {
  const tag = run("git", ["describe", "--tags", "--abbrev=0", "HEAD^"], true);
  return tag || null;
}

function getTagDate(tag: string): string {
  return run("git", ["log", "-1", "--format=%cI", tag]);
}

function getCommitsBetween(fromTag: string, toRef: string): Commit[] {
  const output = run("git", [
    "log",
    `${fromTag}..${toRef}`,
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

  let title = pr.title;
  if (title.endsWith("…")) {
    const commits = runJson<Array<{ commit: { message: string } }>>(
      "gh",
      ["api", `repos/${REPO}/pulls/${pr.number}/commits`],
      true,
    );
    if (commits?.length) {
      const last = commits[commits.length - 1];
      const lastSubject = last.commit.message.split("\n")[0];
      const firstSubject = commits[0].commit.message.split("\n")[0];
      const truncatedPrefix = title.slice(0, -1);
      title = lastSubject.startsWith(truncatedPrefix)
        ? lastSubject
        : firstSubject.startsWith(truncatedPrefix)
          ? firstSubject
          : title;
    }
  }

  return { number: pr.number, title, authorLogin: pr.user.login };
}

function isFirstContributionAfter(
  login: string,
  thresholdDate: string,
): ContributorInfo | null {
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

function generateReleaseNotes(version: string): string {
  const prevTag = getPreviousTag();
  if (!prevTag) {
    throw new Error("No previous tag found. Aborting release-note generation.");
  }

  const prevTagDate = getTagDate(prevTag);
  const commits = getCommitsBetween(prevTag, "HEAD");
  const entries: ChangeEntry[] = [];
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

    const msg = prInfo?.title || commit.message;
    const { group, order } = classifyCommit(msg);

    entries.push({
      hash: commit.hash,
      message: msg,
      author,
      prNumber: prInfo?.number,
      group,
      groupOrder: order,
    });

    if (prInfo?.authorLogin) {
      candidateLogins.add(prInfo.authorLogin);
    }
  }

  const newContributors = Array.from(candidateLogins)
    .map((login) => isFirstContributionAfter(login, prevTagDate))
    .filter((item): item is ContributorInfo => Boolean(item));

  const lines: string[] = [
    '<div align="center">',
    "",
    `[<img alt="Contrabass" width="320px" src="https://github.com/${REPO}/raw/main/.github/assets/contrabass.png" />](https://github.com/${REPO})`,
    "",
    `# \`contrabass@v${version}\` is here!`,
    "",
    "</div>",
  ];

  if (entries.length === 0) {
    lines.push("", "## What's Changed", "", "* No notable changes");
  } else {
    const grouped = new Map<string, ChangeEntry[]>();
    for (const entry of entries) {
      const list = grouped.get(entry.group) || [];
      list.push(entry);
      grouped.set(entry.group, list);
    }

    const sortedGroups = [...grouped.entries()].sort(
      ([, a], [, b]) => (a[0]?.groupOrder ?? 999) - (b[0]?.groupOrder ?? 999),
    );

    for (const [groupTitle, groupEntries] of sortedGroups) {
      lines.push("", `## ${groupTitle}`, "");
      for (const entry of groupEntries.reverse()) {
        const prLink = entry.prNumber
          ? ` in https://github.com/${REPO}/pull/${entry.prNumber}`
          : "";
        const commitLink = entry.prNumber ? "" : ` (${entry.hash.slice(0, 7)})`;
        lines.push(
          `* ${entry.message} by ${entry.author}${prLink}${commitLink}`,
        );
      }
    }
  }

  if (newContributors.length > 0) {
    lines.push("", "## New Contributors", "");
    for (const contributor of newContributors) {
      lines.push(
        `* ${contributor.username} made their first contribution in https://github.com/${REPO}/pull/${contributor.firstPrNumber}`,
      );
    }
  }

  lines.push(
    "",
    `**Full Changelog**: https://github.com/${REPO}/compare/${prevTag}...v${version}`,
  );

  return lines.join("\n");
}

function main(): void {
  const version = process.argv[2];
  if (!version) {
    console.error("Usage: bun scripts/generate-release-notes.ts <version>");
    process.exit(1);
  }
  const notes = generateReleaseNotes(version);
  console.log(notes);
}

main();
