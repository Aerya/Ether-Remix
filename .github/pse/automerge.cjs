// Executed only from protected main, never from a pull request checkout.
const repository = 'Aerya/Ether-Remix';
function isKesurof(pr) {
  return pr.user?.login === 'kesurof' &&
    pr.head?.repo?.full_name === 'kesurof/Ether-Remix' &&
    pr.head?.repo?.owner?.login === 'kesurof' &&
    pr.base?.repo?.full_name === repository && pr.base?.ref === 'main';
}
function needsImage(files) {
  return files.some(({filename}) => !(/\.md$/.test(filename) ||
    filename.startsWith('docs/') || filename.startsWith('release-notes/') ||
    filename.startsWith('.github/') || filename === 'LICENSE'));
}
function canEnable(pr) {
  return isKesurof(pr) && pr.state === 'open' && !pr.draft;
}
async function run({github, context, core, sleep = ms => new Promise(r => setTimeout(r, ms))}) {
  if (`${context.repo.owner}/${context.repo.repo}` !== repository) return;
  const repo = context.repo;
  const source = context.payload.workflow_run;
  let numbers;
  if (source) {
    // A fork can choose a workflow name: also check the trusted path and event.
    if (!['.github/workflows/pse-ci.yml', '.github/workflows/pse-secrets.yml'].includes(source.path) ||
        !['pull_request', 'workflow_dispatch'].includes(source.event) || source.conclusion !== 'success') return;
    const prs = await github.paginate(github.rest.repos.listPullRequestsAssociatedWithCommit,
      {...repo, commit_sha: source.head_sha, per_page: 100});
    numbers = prs.map(pr => pr.number);
  } else {
    numbers = [context.payload.pull_request.number];
  }
  for (const pull_number of numbers) {
    const read = async () => (await github.rest.pulls.get({...repo, pull_number})).data;
    let pr = await read();
    if (!isKesurof(pr)) continue;
    const sha = source?.head_sha || context.payload.pull_request.head.sha;
    if (pr.head.sha !== sha) continue;
    let mergedNow = false;
    if (canEnable(pr)) {
      const permission = (await github.rest.repos.getCollaboratorPermissionLevel({
        ...repo, username: 'kesurof'
      })).data.permission;
      if (!['write', 'admin', 'maintain'].includes(permission)) {
        core.info('Accès écriture non actif : aucune fusion demandée.');
        continue;
      }
      // Native auto-merge respects all protection rules. Pin the live head
      // against races; no administrator or bypass option is ever used.
      if (!pr.auto_merge) {
        try {
          await github.graphql(`mutation($id: ID!, $head: GitObjectID!) {
            enablePullRequestAutoMerge(input: {pullRequestId: $id, expectedHeadOid: $head, mergeMethod: SQUASH}) {
              pullRequest { number }
            }
          }`, {id: pr.node_id, head: sha});
        } catch (error) {
          // Like gh pr merge --auto: GitHub refuses to enable auto-merge if
          // all requirements already passed. A normal merge still enforces
          // every branch rule and the expected head SHA; it cannot bypass CI.
          if (!error.message.includes('clean status')) throw error;
          await github.rest.pulls.merge({...repo, pull_number, sha, merge_method: 'squash'});
          mergedNow = true;
        }
      }
    }
    // GITHUB_TOKEN merges do not emit ordinary workflow triggers. workflow_run
    // still arrives for both required checks; explicitly dispatch Docker only
    // after GitHub confirms a protected merge. No fork content is executed here.
    if (!source && !mergedNow) continue;
    for (let attempt = 0; attempt < 15; attempt++) {
      pr = await read();
      if (pr.merged || pr.state !== 'open' || pr.head.sha !== sha) break;
      if (!pr.auto_merge) break;
      await sleep(2000);
    }
    if (!pr.merged || pr.head.sha !== sha) continue;
    const files = await github.paginate(github.rest.pulls.listFiles, {...repo, pull_number, per_page: 100});
    if (!needsImage(files)) continue;
    const mergeSha = pr.merge_commit_sha;
    const existing = await github.rest.actions.listWorkflowRuns({
      ...repo, workflow_id: 'pse-docker.yml', head_sha: mergeSha, per_page: 100
    });
    if (existing.data.workflow_runs.length) continue;
    const main = (await github.rest.repos.getBranch({...repo, branch: 'main'})).data;
    if (main.commit.sha !== mergeSha) {
      // A later merge already owns publication of the current main.
      core.info('main a avancé : la publication du dernier commit prend le relais.');
      continue;
    }
    await github.rest.actions.createWorkflowDispatch({...repo, workflow_id: 'pse-docker.yml', ref: 'main'});
    core.info(`Publication Docker demandée après fusion de la PR #${pull_number}.`);
  }
}
module.exports = {run, isKesurof, canEnable, needsImage};
