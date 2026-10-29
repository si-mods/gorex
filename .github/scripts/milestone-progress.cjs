'use strict';

// Progress for the roadmap issue: a merged pull request says which
// checklist items it completed, and this checks the same items in the
// issue. A PR body opts in with a line such as:
//
//   Progress: #35 M0
//   - [x] Introduce a stable action registry ...
//
// Only checked items are copied, only within the named milestone, and
// items are never unchecked. The workflow runs on pull_request_target with
// the base branch checked out, so it never runs code from the PR.

const progressLine = /^\s*Progress:\s*#(\d+)\s+([A-Za-z0-9]+)\s*$/im;
const checkedLine = /^\s*[-*]\s*\[[xX]\]\s+(.+?)\s*$/;
const taskLine = /^(\s*)- \[([ xX])\] (.+?)\s*$/;
const sectionLine = /^##\s+(M\d+)\b/;

// stripComments removes HTML comments, so that a template's example does
// not count as progress.
function stripComments(text) {
  return String(text || '').replace(/<!--[\s\S]*?-->/g, '');
}

// normalize compares an item as a reader sees it, without the whitespace
// that wrapping and copying change.
function normalize(text) {
  return text.replace(/\s+/g, ' ').trim().toLowerCase();
}

// parseProgress returns the issue and milestone a PR body names.
function parseProgress(prBody) {
  const m = (prBody || '').match(progressLine);
  if (!m) return null;
  return { issue: Number(m[1]), milestone: m[2].toUpperCase() };
}

// checkedItems returns the normalized text of the checked items in a PR
// body.
function checkedItems(prBody) {
  const items = new Set();
  for (const line of (prBody || '').split(/\r?\n/)) {
    const m = line.match(checkedLine);
    if (m) items.add(normalize(m[1]));
  }
  return items;
}

// applyProgress checks in issueBody the unchecked items that items names,
// within milestone ("M0", or "" for every milestone). It returns the new
// body and the counts before and after.
function applyProgress(issueBody, items, milestone) {
  const lines = String(issueBody || '').split('\n');
  let section = '';
  let changed = 0;
  let total = 0;
  let doneBefore = 0;
  let doneAfter = 0;

  for (let i = 0; i < lines.length; i++) {
    const heading = lines[i].match(sectionLine);
    if (heading) {
      section = heading[1].toUpperCase();
      continue;
    }
    if (milestone && section !== milestone) continue;
    const task = lines[i].match(taskLine);
    if (!task) continue;
    total++;
    if (task[2].toLowerCase() === 'x') {
      doneBefore++;
      doneAfter++;
      continue;
    }
    if (!items.has(normalize(task[3]))) continue;
    lines[i] = `${task[1]}- [x] ${task[3]}`;
    changed++;
    doneAfter++;
  }

  return {
    body: lines.join('\n'),
    changed,
    total,
    doneBefore,
    doneAfter,
    milestone,
  };
}

// pullRequestForEvent returns the merged pull request of a
// pull_request_target event, or the one a push's commit came from.
async function pullRequestForEvent({ github, context }) {
  const owner = context.repo.owner;
  const repo = context.repo.repo;
  if (context.eventName === 'workflow_dispatch') {
    const n = Number(context.payload.inputs && context.payload.inputs.pr);
    if (!n) return null;
    const { data: pr } = await github.rest.pulls.get({ owner, repo, pull_number: n });
    return pr.merged_at ? pr : null;
  }
  if (context.payload.pull_request) return context.payload.pull_request;
  const sha = (context.payload.head_commit && context.payload.head_commit.id) || context.sha;
  if (!sha) return null;
  const { data: prs } = await github.rest.repos.listPullRequestsAssociatedWithCommit({
    owner,
    repo,
    commit_sha: sha,
  });
  const pr = prs.find((p) => p.merged_at);
  if (pr) return pr;

  // A merge commit's message names its pull request when the API does not
  // associate the merge commit with it.
  const message = (context.payload.head_commit && context.payload.head_commit.message) || '';
  const m = message.match(/Merge pull request #(\d+)/) || message.match(/\(#(\d+)\)/);
  if (!m) return null;
  try {
    const { data: named } = await github.rest.pulls.get({
      owner,
      repo,
      pull_number: Number(m[1]),
    });
    return named.merged_at ? named : null;
  } catch (err) {
    // A commit message's (#N) may name an issue, not a pull request.
    return null;
  }
}

// update is the workflow body: it reads the merged PR, updates the issue,
// and comments once when the milestone becomes complete.
async function update({ github, context, core }) {
  const pr = await pullRequestForEvent({ github, context });
  if (!pr || (pr.merged !== true && !pr.merged_at)) return;

  // The merge is the authorization: this only runs for a merged PR, or a
  // manual replay of one, and edits only ROADMAP_ISSUE.
  const body = stripComments(pr.body);
  const progress = parseProgress(body);
  if (!progress) {
    core.info('No "Progress: #issue milestone" line; nothing to update.');
    return;
  }
  const only = Number(process.env.ROADMAP_ISSUE || 0);
  if (only && progress.issue !== only) {
    core.warning(`Progress names #${progress.issue}, not the roadmap issue #${only}.`);
    return;
  }
  const items = checkedItems(body);
  if (items.size === 0) {
    core.warning('The PR names an issue but has no checked items to copy.');
    return;
  }

  const owner = context.repo.owner;
  const repo = context.repo.repo;
  let issue;
  try {
    ({ data: issue } = await github.rest.issues.get({
      owner,
      repo,
      issue_number: progress.issue,
    }));
  } catch (err) {
    core.warning(`Cannot read issue #${progress.issue}: ${err.message}`);
    return;
  }
  if (issue.pull_request) {
    core.warning(`#${progress.issue} is a pull request; progress updates issues only.`);
    return;
  }
  if (issue.state !== 'open') {
    core.warning(`Issue #${progress.issue} is ${issue.state}; progress updates open issues only.`);
    return;
  }
  if (!issue.body) {
    core.warning(`Issue #${progress.issue} has no body.`);
    return;
  }

  const result = applyProgress(issue.body, items, progress.milestone);
  if (result.total === 0) {
    core.warning(
      `No checklist items in ${progress.milestone || 'the issue'} of #${progress.issue}.`
    );
    return;
  }
  if (result.changed === 0) {
    core.info('The issue already has every matching item checked.');
    return;
  }

  await github.rest.issues.update({
    owner,
    repo,
    issue_number: progress.issue,
    body: result.body,
  });
  core.info(
    `Checked ${result.changed} item(s): ${result.doneAfter}/${result.total} in ${progress.milestone || 'the issue'}.`
  );

  if (result.doneBefore < result.total && result.doneAfter === result.total) {
    const label = progress.milestone ? `\`${progress.milestone}\`` : 'The roadmap';
    await github.rest.issues.createComment({
      owner,
      repo,
      issue_number: progress.issue,
      body: `${label} is complete with #${pr.number}.`,
    });
  }
}

module.exports = {
  stripComments,
  normalize,
  parseProgress,
  checkedItems,
  applyProgress,
  pullRequestForEvent,
  update,
};
