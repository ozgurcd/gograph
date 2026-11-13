package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the exact proof consumer with an offline GitHub API fixture.
// CI and Release both call test-proof.yml, so there is one consumer to test.
// No checkout, shell, temporary directory, cleanup, or network is involved.
func TestCIProofProducerBinding(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("node is required for the workflow API fixtures")
	}
	data, err := os.ReadFile(filepath.Join("../..", ".github/workflows", "test-proof.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	inScript := false
	for _, line := range strings.Split(string(data), "\n") {
		if line == "          script: |" {
			if inScript || len(lines) > 0 {
				t.Fatal("test-proof.yml has more than one consumer")
			}
			inScript = true
			continue
		}
		if inScript {
			if line != "" && !strings.HasPrefix(line, "            ") {
				inScript = false
				continue
			}
			lines = append(lines, strings.TrimPrefix(line, "            "))
		}
	}
	if len(lines) == 0 {
		t.Fatal("test-proof.yml has no consumer")
	}
	cmd := exec.Command(node, "-e", ciProofAPIFixture)
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("offline API fixtures: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}

// The consumer must exist exactly once: a second copy could drift from the tested one.
func TestProofConsumerIsNotDuplicated(t *testing.T) {
	for _, name := range []string{"ci.yml", "release.yml", "checks.yml"} {
		data, err := os.ReadFile(filepath.Join("../..", ".github/workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "actions/github-script") {
			t.Errorf("%s contains inline script; the proof consumer belongs only in test-proof.yml", name)
		}
	}
}

const ciProofAPIFixture = `
const assert = require('node:assert/strict');
const fs = require('node:fs');
const AsyncFunction = Object.getPrototypeOf(async function(){}).constructor;
const consumer = new AsyncFunction('github', 'context', 'core', fs.readFileSync(0, 'utf8'));
const sha = c => c.repeat(40);
const H = sha('a'), T = sha('b'), M = sha('c'), P = sha('d'), F = sha('e');
const full_name = 'ozgurcd/gograph';
const cases = [];
const test = (name, want, change = () => {}) => cases.push({name, want, change});
test('trusted main push', true);
test('PR title and second-parent forgery', false, f => {
  f.run.event = 'pull_request'; f.run.head_branch = 'attack'; f.run.head_sha = P;
  f.jobs[1].name = 'tested-commit-' + F;
  f.commits[F] = {tree:{sha:T}, parents:[{sha:M},{sha:P}]};
  f.policy[F] = M;
  f.policy[P] = sha('f'); // actual producer workflow differs; claimed commit is trusted
});
test('PR run not associated with the target', false, f => { f.run.event = 'pull_request'; });
const R = sha('1'), PH = sha('2');
const asPR = f => {
  f.run.event = 'pull_request'; f.run.head_branch = 'feature'; f.run.head_sha = PH;
  f.prs = [{base:{ref:'main'}, head:{sha:PH, repo:{full_name}}}];
  f.runs = []; f.prRuns = [{id:1}];
  f.policy[PH] = M; f.policy[R] = M;
  f.commits[R] = {tree:{sha:T}, parents:[{sha:M},{sha:PH}]};
  f.jobs[1].name = 'tested-commit-' + R;
};
const pr = (name, want, change = () => {}) => test(name, want, f => { asPR(f); change(f); });
pr('PR run reused by tree', true);
pr('PR tree mismatch', false, f => { f.commits[R].tree.sha = P; });
pr('PR merge commit not a child of the run head', false, f => { f.commits[R].parents = [{sha:M},{sha:P}]; });
pr('PR merge commit with one parent', false, f => { f.commits[R].parents = [{sha:M}]; });
pr('PR merge commit policy differs', false, f => { f.policy[R] = sha('f'); });
pr('PR head policy differs', false, f => { f.policy[PH] = sha('f'); });
pr('PR merge commit missing', false, f => { delete f.commits[R]; });
pr('PR merge commit policy file missing', false, f => { delete f.policy[R]; });
pr('PR head not associated with target', false, f => { f.prs = []; });
pr('PR associated head from fork', false, f => { f.prs[0].head.repo.full_name = 'fork/gograph'; });
pr('PR associated into another base', false, f => { f.prs[0].base.ref = 'other'; });
pr('PR run from fork', false, f => { f.run.head_repository.full_name = 'fork/gograph'; });
pr('PR run head differs from association', false, f => { f.run.head_sha = P; });
pr('PR marker names unrelated main commit', false, f => {
  f.jobs[1].name = 'tested-commit-' + H; f.policy[H] = M; f.commits[H] = {tree:{sha:T}, parents:[{sha:M}]};
});
pr('PR marker expression', false, f => { f.jobs[1].name = 'tested-commit-${{ github.event.pull_request.title }}'; });
pr('PR duplicate marker', false, f => { f.jobs.push({...f.jobs[1], id:3}); });
pr('PR failed race step', false, f => { f.jobs[0].steps[0].conclusion = 'failure'; });
pr('PR skipped race step', false, f => { f.jobs[0].steps[0].conclusion = 'skipped'; });
pr('PR failed marker', false, f => { f.jobs[1].conclusion = 'failure'; });
pr('PR rerun changes attempt', false, f => { f.final = {...f.run, run_attempt:2}; });
pr('PR association lookup fails', false, f => { f.fail = 'prs'; });
test('push association lookup fails', false, f => { f.fail = 'prs'; });
const O = sha('9');
const older = f => { f.runs = [{id:2}, {id:1}]; f.extra = {2: {...f.run, id:2, head_sha:O}}; f.commits[O] = {tree:{sha:T}, parents:[{sha:M}]}; };
test('older run without policy file is skipped', true, older);
test('older run policy read fails', false, f => { older(f); f.policyError = {[O]: 500}; });
test('trusted main policy file missing', false, f => { delete f.policy[M]; });
test('schedule producer', false, f => { f.run.event = 'schedule'; });
test('non-main push', false, f => { f.run.head_branch = 'other'; });
test('changed producer workflow', false, f => { f.policy[H] = sha('f'); });
test('changed producer reusable workflow', false, f => { f.changedPolicy = 'test-proof.yml'; });
test('changed producer checks workflow', false, f => { f.changedPolicy = 'checks.yml'; });
test('wrong producer path', false, f => { f.run.path = '.github/workflows/other.yml'; });
test('fork producer', false, f => { f.run.head_repository.full_name = 'fork/gograph'; });
test('wrong repository', false, f => { f.run.repository.full_name = 'other/gograph'; });
test('forged marker name', false, f => { f.jobs[1].name = 'tested-commit-' + F; });
test('marker title expression', false, f => { f.jobs[1].name = 'tested-commit-${{ github.event.pull_request.title }}'; });
test('duplicate marker', false, f => { f.jobs.push({...f.jobs[1], id:3}); });
test('tree mismatch', false, f => { f.commits[H].tree.sha = P; });
test('head missing', false, f => { delete f.run.head_sha; });
test('head malformed', false, f => { f.run.head_sha = 'title'; });
test('unknown producer commit', false, f => { delete f.commits[H]; });
test('workflow unavailable', false, f => { delete f.policy[H]; });
test('failed producer', false, f => { f.run.conclusion = 'failure'; });
test('failed race step', false, f => { f.jobs[0].steps[0].conclusion = 'failure'; });
test('skipped race step', false, f => { f.jobs[0].steps[0].conclusion = 'skipped'; });
test('failed marker', false, f => { f.jobs[1].conclusion = 'failure'; });
test('rerun changes attempt', false, f => { f.final = {...f.run, run_attempt:2}; });
test('final metadata changes head', false, f => { f.final = {...f.run, head_sha:P}; });
test('missing attempt', false, f => { delete f.run.run_attempt; });
test('API failure', false, f => { f.fail = 'runs1'; });
test('PR consumer makes no API calls', false, f => { f.event = 'pull_request'; f.fail = 'any'; });
const laterRuns = f => { f.runs = [...Array.from({length:100}, (_,i) => ({id:i+10})), {id:1}]; };
const laterJobs = f => { f.jobs = [...Array.from({length:100}, (_,i) => ({id:i+10, name:'other'})), ...f.jobs]; };
test('run on page two', true, laterRuns);
test('jobs on page two', true, laterJobs);
test('duplicate marker across pages', false, f => { laterJobs(f); f.jobs[0] = {...f.jobs[101], id:10}; });
test('run page two error after valid first page', false, f => { laterRuns(f); f.runs.reverse(); f.fail = 'runs2'; });
test('job page two error', false, f => { laterJobs(f); f.fail = 'jobs2'; });
test('run total changes', false, f => { laterRuns(f); f.total = (key,page,n) => key === 'runs' && page === 2 ? n+1 : n; });
test('job total changes', false, f => { laterJobs(f); f.total = (key,page,n) => key === 'jobs' && page === 2 ? n+1 : n; });
test('missing total', false, f => { f.total = () => undefined; });
test('noninteger total', false, f => { f.total = () => 1.5; });
test('short run page', false, f => { f.total = (key,page,n) => key === 'runs' ? n+100 : n; });
test('short job page', false, f => { f.total = (key,page,n) => key === 'jobs' ? n+100 : n; });
test('total under reported rows', false, f => { f.total = () => 0; });
test('cap exceeded', false, f => { f.total = () => 1001; });
test('duplicate run across pages', false, f => { laterRuns(f); f.runs[0] = {id:1}; });
test('duplicate job across pages', false, f => { laterJobs(f); f.jobs[0].id = 1; });
test('complete run list at cap', true, f => {
  f.runs = [...Array.from({length:499}, (_,i) => ({id:i+10})), {id:1}];
});
test('complete job list at cap', true, f => {
  f.jobs = [...Array.from({length:498}, (_,i) => ({id:i+10,name:'other'})), ...f.jobs];
});
test('empty run list', false, f => { f.runs = []; });
test('empty job list', false, f => { f.jobs = []; });
test('missing job total', false, f => { f.total = (key,page,n) => key === 'jobs' ? undefined : n; });
test('job cap exceeded', false, f => { f.total = (key,page,n) => key === 'jobs' ? 501 : n; });
test('missing marker', false, f => { f.jobs.pop(); });
test('suite job from another caller', false, f => { f.jobs[0].name = 'other / verify'; });
test('legacy suite job name', false, f => { f.jobs[0].name = 'build-and-test'; });
test('duplicate race step', false, f => { f.jobs[0].steps.push({...f.jobs[0].steps[0]}); });
test('run detail identity mismatch', false, f => { f.run.id = 2; });
(async () => {
 for (const {name,want,change} of cases) {
  const run = {id:1, status:'completed', conclusion:'success', event:'push', head_branch:'main',
    path:'.github/workflows/ci.yml', head_sha:H, run_attempt:1,
    repository:{full_name}, head_repository:{full_name}};
  const f = {run, event:'push', runs:[{id:1}], prRuns:[], prs:[], policy:{[H]:M,[M]:M}, calls:[],
    commits:{[H]:{tree:{sha:T},parents:[{sha:M}]}},
    jobs:[{id:1,name:'checks / verify',conclusion:'success',steps:[{name:'Run Race Tests',conclusion:'success'}]},
          {id:2,name:'tested-commit-'+H,conclusion:'success'}], total:(key,page,n)=>n};
  change(f);
  const check = key => { f.calls.push(key); if (f.fail === key || f.fail === 'any') throw Error('API failure'); };
  const listing = (key, rows, page=1) => {
    check(key+page);
    return {data:{total_count:f.total(key,page,rows.length),
      [key === 'runs' ? 'workflow_runs' : 'jobs']: rows.slice((page-1)*100,page*100)}};
  };
  const github = {rest:{git:{getCommit:async ({commit_sha})=>{
    check('commit'); if (!f.commits[commit_sha]) throw Object.assign(Error('Not Found'), {status:404});
    return {data:f.commits[commit_sha]};
  }},repos:{getCommit:async ({ref})=>{check('ref');return {data:{sha:M,commit:{tree:{sha:T}}}};},
    listPullRequestsAssociatedWithCommit:async ({commit_sha})=>{check('prs');assert.equal(commit_sha,M);return {data:f.prs};},
    getContent:async ({ref,path})=>{
      check('blob');
      if (f.policyError?.[ref]) throw Object.assign(Error('Server Error'), {status:f.policyError[ref]});
      if (!f.policy[ref]) throw Object.assign(Error('Not Found'), {status:404});
      assert.ok(['.github/workflows/ci.yml','.github/workflows/test-proof.yml','.github/workflows/checks.yml'].includes(path));
      return {data:{type:'file',sha:f.changedPolicy && ref === H && path.endsWith(f.changedPolicy) ? P : f.policy[ref]}};
    }
  },actions:{listWorkflowRuns:async ({page,event})=>listing('runs',event==='pull_request'?f.prRuns:f.runs,page),
    getWorkflowRun:async ({run_id})=>{check('run');if(run_id!==1)return {data:structuredClone(f.extra?.[run_id]??{id:run_id,status:'failure'})};
      f.reads=(f.reads||0)+1;return {data:structuredClone(f.reads>1&&f.final ? f.final : f.run)};}
  }},request:async (route,args)=>{assert.equal(args.attempt_number,1);return listing('jobs',f.jobs,args.page);}};
  const outputs = {};
  await consumer(github,{repo:{owner:'ozgurcd',repo:'gograph'},eventName:f.event,runId:999},
    {setOutput:(k,v)=>outputs[k]=v,info:()=>{},warning:()=>{}});
  assert.equal(outputs.verified, String(want), name);
  if (f.event === 'pull_request') assert.deepEqual(f.calls, [], name);
 }
 console.log(cases.length+' offline API fixture cases passed');
})().catch(error=>{console.error(error);process.exitCode=1;});
`
