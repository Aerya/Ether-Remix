const {test} = require('node:test');
const assert = require('node:assert/strict');
const {run, isKesurof, canEnable, needsImage} = require('./automerge.cjs');
const repo = {full_name: 'Aerya/Ether-Remix'};
const fixture = () => ({number: 1, node_id: 'PR_test', state: 'open', draft: false,
  user: {login: 'kesurof'}, head: {sha: 'abc', repo: {full_name: 'kesurof/Ether-Remix', owner: {login: 'kesurof'}}},
  base: {ref: 'main', repo: structuredClone(repo)}, auto_merge: null});
function harness(pr, event = 'pull_request_target') {
  const calls = [];
  const context = {repo: {owner: 'Aerya', repo: 'Ether-Remix'}, payload: {pull_request: structuredClone(pr)}};
  if (event === 'workflow_run') {
    context.payload = {workflow_run: {head_sha: 'abc', path: '.github/workflows/pse-ci.yml', event: 'pull_request', conclusion: 'success'}};
  }
  const github = {
    paginate: async method => method === 'files' ? [{filename: 'web/src/example.tsx'}] : [{number: 1}],
    graphql: async (...args) => { calls.push(['enable', ...args]); },
    rest: {
      pulls: {get: async () => ({data: pr}), listFiles: 'files'},
      repos: {
        listPullRequestsAssociatedWithCommit: {},
        getCollaboratorPermissionLevel: async () => ({data: {permission: 'write'}}),
        getBranch: async () => ({data: {commit: {sha: 'merged'}}})
      },
      actions: {
        listWorkflowRuns: async () => ({data: {workflow_runs: []}}),
        createWorkflowDispatch: async value => calls.push(['dispatch', value])
      }
    }
  };
  return {calls, context, github, core: {info() {}}, sleep: async () => {}};
}
test('seul le compte, fork et main exacts sont éligibles', () => {
  assert.equal(isKesurof(fixture()), true);
  for (const change of [p => p.user.login = 'other', p => p.head.repo.full_name = 'other/Ether-Remix',
    p => p.head.repo.owner.login = 'other', p => p.base.ref = 'dev', p => p.base.repo.full_name = 'other/repo']) {
    const pr = fixture(); change(pr); assert.equal(isKesurof(pr), false);
  }
});
test('brouillons et PR fermées ne sont jamais activés', async () => {
  for (const change of [p => p.draft = true, p => p.state = 'closed', p => p.user.login = 'Aerya']) {
    const pr = fixture(); change(pr); assert.equal(canEnable(pr), false);
    const h = harness(pr); await run(h); assert.deepEqual(h.calls, []);
  }
});
test('activation native avec SHA attendu, sans fusion directe ni bypass', async () => {
  const h = harness(fixture()); await run(h);
  assert.equal(h.calls.length, 1);
  assert.match(h.calls[0][1], /enablePullRequestAutoMerge/);
  assert.deepEqual(h.calls[0][2], {id: 'PR_test', head: 'abc'});
});
test('un événement ancien et une permission retirée sont ignorés', async () => {
  const pr = fixture(); const h = harness(pr); pr.head.sha = 'new'; await run(h);
  assert.deepEqual(h.calls, []);
  const other = harness(fixture());
  other.github.rest.repos.getCollaboratorPermissionLevel = async () => ({data: {permission: 'read'}});
  await run(other); assert.deepEqual(other.calls, []);
});
test('aucune publication avec CI en échec, conflit, ou PR non fusionnée', async () => {
  const pr = fixture(); pr.auto_merge = {}; const h = harness(pr, 'workflow_run');
  await run(h); assert.deepEqual(h.calls, []);
  h.context.payload.workflow_run.conclusion = 'failure'; await run(h); assert.deepEqual(h.calls, []);
  h.context.payload.workflow_run.conclusion = 'success'; h.context.payload.workflow_run.path = 'fake.yml';
  await run(h); assert.deepEqual(h.calls, []);
});
test('publication après fusion confirmée ; aucun doublon si déjà construite', async () => {
  const pr = fixture(); Object.assign(pr, {merged: true, state: 'closed', merge_commit_sha: 'merged'});
  const h = harness(pr, 'workflow_run'); await run(h);
  assert.deepEqual(h.calls, [['dispatch', {owner: 'Aerya', repo: 'Ether-Remix', workflow_id: 'pse-docker.yml', ref: 'main'}]]);
  h.calls.length = 0;
  h.github.rest.actions.listWorkflowRuns = async () => ({data: {workflow_runs: [{}]}});
  await run(h); assert.deepEqual(h.calls, []);
});

test('si tout est déjà vert, fusion native protégée et publication, sans bypass', async () => {
  const pr = fixture(); const h = harness(pr);
  h.github.graphql = async () => { throw new Error('Pull request is in clean status'); };
  h.github.rest.pulls.merge = async value => {
    assert.deepEqual(value, {owner: 'Aerya', repo: 'Ether-Remix', pull_number: 1, sha: 'abc', merge_method: 'squash'});
    Object.assign(pr, {merged: true, state: 'closed', merge_commit_sha: 'merged'});
  };
  await run(h); assert.equal(h.calls[0][0], 'dispatch');
  const rejected = harness(fixture());
  rejected.github.graphql = async () => { throw new Error('Required status check is missing'); };
  await assert.rejects(run(rejected), /Required status check/);
  assert.deepEqual(rejected.calls, []);
});

test('aucune image pour la configuration GitHub et la documentation seules', async () => {
  assert.equal(needsImage([{filename: '.github/workflows/pse-ci.yml'}, {filename: 'README.md'}]), false);
  assert.equal(needsImage([{filename: '.github/pse/automerge.cjs'}, {filename: 'internal/app/example.go'}]), true);
  const pr = fixture(); Object.assign(pr, {merged: true, state: 'closed', merge_commit_sha: 'merged'});
  const h = harness(pr, 'workflow_run');
  h.github.paginate = async method => method === 'files' ? [{filename: '.github/workflows/pse-ci.yml'}] : [{number: 1}];
  await run(h); assert.deepEqual(h.calls, []);
});
