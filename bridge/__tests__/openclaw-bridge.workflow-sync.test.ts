import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  cronJobToWorkflowSchedule,
  projectMatchesCronJob,
  resetWorkflowSyncStateForTest,
  shouldTreatAsSystemWorkflow,
  syncWorkflowProjectsFromCronJobsForTest,
  workflowTemplateForCronJob,
} from '../openclaw-bridge';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function requestURL(input: RequestInfo | URL): string {
  if (typeof input === 'string') {
    return input;
  }
  if (input instanceof URL) {
    return input.toString();
  }
  return input.url;
}

function requestMethod(input: RequestInfo | URL, init?: RequestInit): string {
  if (init?.method) {
    return String(init.method).toUpperCase();
  }
  if (typeof input !== 'string' && !(input instanceof URL)) {
    return input.method.toUpperCase();
  }
  return 'GET';
}

describe('cronJobToWorkflowSchedule', () => {
  it('maps cron expressions (with tz suffix) to workflow cron schedule payload', () => {
    assert.deepEqual(
      cronJobToWorkflowSchedule({
        id: 'cron-job-1',
        name: 'Morning Briefing',
        schedule: '0 6 * * * (America/Denver)',
        enabled: true,
      }),
      {
        kind: 'cron',
        expr: '0 6 * * *',
        tz: 'America/Denver',
        cron_id: 'cron-job-1',
      },
    );
  });

  it('maps duration schedules to everyMs payload', () => {
    assert.deepEqual(
      cronJobToWorkflowSchedule({
        id: 'cron-job-2',
        schedule: '15m',
        enabled: true,
      }),
      {
        kind: 'every',
        everyMs: 900000,
        cron_id: 'cron-job-2',
      },
    );
  });
});

describe('workflowTemplateForCronJob', () => {
  it('uses pipeline none for system workflow names', () => {
    const template = workflowTemplateForCronJob({
      id: 'cron-job-heartbeat',
      name: 'System: Heartbeat',
      enabled: true,
    });
    assert.equal(template.pipeline, 'none');
    assert.equal(template.auto_close, false);
    assert.deepEqual(template.labels, ['automated']);
  });

  it('uses auto_close pipeline for non-system workflow names', () => {
    const template = workflowTemplateForCronJob({
      id: 'cron-job-briefing',
      name: 'Morning Briefing',
      enabled: true,
    });
    assert.equal(template.pipeline, 'auto_close');
    assert.equal(template.auto_close, true);
  });
});

describe('projectMatchesCronJob', () => {
  it('matches by embedded cron_id in workflow schedule', () => {
    const match = projectMatchesCronJob(
      {
        id: 'project-1',
        name: 'Some Name',
        workflow_schedule: { kind: 'cron', expr: '0 6 * * *', cron_id: 'job-123' },
      },
      {
        id: 'job-123',
        name: 'Different Name',
        enabled: true,
      },
    );
    assert.equal(match, true);
  });

  it('falls back to name matching when cron_id is absent', () => {
    const match = projectMatchesCronJob(
      {
        id: 'project-2',
        name: 'Morning Briefing',
        workflow_schedule: { kind: 'cron', expr: '0 6 * * *' },
      },
      {
        id: 'job-456',
        name: 'Morning Briefing',
        enabled: true,
      },
    );
    assert.equal(match, true);
  });
});

describe('shouldTreatAsSystemWorkflow', () => {
  it('classifies known system workflow names', () => {
    assert.equal(shouldTreatAsSystemWorkflow('System: Heartbeat'), true);
    assert.equal(shouldTreatAsSystemWorkflow('Agent Health Sweep'), true);
    assert.equal(shouldTreatAsSystemWorkflow('Morning Briefing'), false);
  });
});

describe('syncWorkflowProjectsFromCronJobsForTest', () => {
  it('does not trigger workflow runs on first sync', async () => {
    resetWorkflowSyncStateForTest();
    const originalFetch = globalThis.fetch;
    const calls: Array<{ method: string; url: string }> = [];
    try {
      globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        calls.push({ method, url });
        if (method === 'GET' && url.includes('/api/projects?workflow=true')) {
          return jsonResponse({ projects: [] });
        }
        if (method === 'POST' && url.endsWith('/api/projects')) {
          return jsonResponse({
            id: 'project-1',
            name: 'Morning Briefing',
            workflow_schedule: { kind: 'cron', expr: '0 6 * * *', cron_id: 'job-1' },
          }, 201);
        }
        if (method === 'PATCH' && url.includes('/api/projects/project-1')) {
          return jsonResponse({}, 200);
        }
        if (method === 'POST' && url.includes('/runs/trigger')) {
          return jsonResponse({}, 201);
        }
        throw new Error(`unexpected request ${method} ${url}`);
      }) as typeof fetch;

      await syncWorkflowProjectsFromCronJobsForTest([
        {
          id: 'job-1',
          name: 'Morning Briefing',
          schedule: '0 6 * * * (America/Denver)',
          enabled: true,
          last_run_at: '2026-02-09T12:00:00Z',
        },
      ]);

      const runTriggers = calls.filter((call) => call.url.includes('/runs/trigger'));
      assert.equal(runTriggers.length, 0);
    } finally {
      globalThis.fetch = originalFetch;
      resetWorkflowSyncStateForTest();
    }
  });

  it('deduplicates duplicate cron fire triggers by cron id + last_run_at', async () => {
    resetWorkflowSyncStateForTest();
    const originalFetch = globalThis.fetch;
    const calls: Array<{ method: string; url: string }> = [];
    try {
      globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        calls.push({ method, url });
        if (method === 'GET' && url.includes('/api/projects?workflow=true')) {
          return jsonResponse({
            projects: [
              {
                id: 'project-1',
                name: 'Morning Briefing',
                workflow_schedule: { kind: 'cron', expr: '0 6 * * *', cron_id: 'job-1' },
              },
            ],
          });
        }
        if (method === 'PATCH' && url.includes('/api/projects/project-1')) {
          return jsonResponse({}, 200);
        }
        if (method === 'POST' && url.includes('/runs/trigger')) {
          return jsonResponse({}, 201);
        }
        throw new Error(`unexpected request ${method} ${url}`);
      }) as typeof fetch;

      await syncWorkflowProjectsFromCronJobsForTest([
        {
          id: 'job-1',
          name: 'Morning Briefing',
          schedule: '0 6 * * * (America/Denver)',
          enabled: true,
          last_run_at: '2026-02-09T12:00:00Z',
        },
      ]);
      await syncWorkflowProjectsFromCronJobsForTest([
        {
          id: 'job-1',
          name: 'Morning Briefing',
          schedule: '0 6 * * * (America/Denver)',
          enabled: true,
          last_run_at: '2026-02-09T12:00:00Z',
        },
      ]);
      await syncWorkflowProjectsFromCronJobsForTest([
        {
          id: 'job-1',
          name: 'Morning Briefing',
          schedule: '0 6 * * * (America/Denver)',
          enabled: true,
          last_run_at: '2026-02-09T12:05:00Z',
        },
      ]);
      await syncWorkflowProjectsFromCronJobsForTest([
        {
          id: 'job-1',
          name: 'Morning Briefing',
          schedule: '0 6 * * * (America/Denver)',
          enabled: true,
          last_run_at: '2026-02-09T12:05:00Z',
        },
      ]);

      const runTriggers = calls.filter((call) => call.url.includes('/runs/trigger'));
      assert.equal(runTriggers.length, 1);
    } finally {
      globalThis.fetch = originalFetch;
      resetWorkflowSyncStateForTest();
    }
  });

  it('handles create-project failures without aborting sync loop', async () => {
    resetWorkflowSyncStateForTest();
    const originalFetch = globalThis.fetch;
    const calls: Array<{ method: string; url: string }> = [];
    try {
      globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = requestURL(input);
        const method = requestMethod(input, init);
        calls.push({ method, url });
        if (method === 'GET' && url.includes('/api/projects?workflow=true')) {
          return jsonResponse({ projects: [] });
        }
        if (method === 'POST' && url.endsWith('/api/projects')) {
          return new Response('create failed', { status: 400, statusText: 'Bad Request' });
        }
        if (method === 'PATCH' && url.includes('/api/projects/')) {
          return jsonResponse({}, 200);
        }
        if (method === 'POST' && url.includes('/runs/trigger')) {
          return jsonResponse({}, 201);
        }
        throw new Error(`unexpected request ${method} ${url}`);
      }) as typeof fetch;

      await syncWorkflowProjectsFromCronJobsForTest([
        {
          id: 'job-1',
          name: 'Morning Briefing',
          schedule: '0 6 * * * (America/Denver)',
          enabled: true,
          last_run_at: '2026-02-09T12:00:00Z',
        },
      ]);

      const createCalls = calls.filter((call) => call.method === 'POST' && call.url.endsWith('/api/projects'));
      const patchCalls = calls.filter((call) => call.method === 'PATCH' && call.url.includes('/api/projects/'));
      const triggerCalls = calls.filter((call) => call.url.includes('/runs/trigger'));

      assert.equal(createCalls.length, 1);
      assert.equal(patchCalls.length, 0);
      assert.equal(triggerCalls.length, 0);
    } finally {
      globalThis.fetch = originalFetch;
      resetWorkflowSyncStateForTest();
    }
  });
});
