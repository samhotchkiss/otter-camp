import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  cronJobToWorkflowSchedule,
  projectMatchesCronJob,
  shouldTreatAsSystemWorkflow,
  workflowTemplateForCronJob,
} from '../openclaw-bridge';

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
    assert.equal(template.auto_close, true);
    assert.deepEqual(template.labels, ['automated']);
  });

  it('uses auto_close pipeline for non-system workflow names', () => {
    const template = workflowTemplateForCronJob({
      id: 'cron-job-briefing',
      name: 'Morning Briefing',
      enabled: true,
    });
    assert.equal(template.pipeline, 'auto_close');
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
