-- Phase 7F Approval UX: Add waiting_for_approval status for async polling.
-- Async routed workflows that hit the approval gate now report
-- "waiting_for_approval" instead of "running" so the poll API can
-- surface approval_url / approval_id to the caller.

-- Drop the old CHECK constraint and recreate it with the new value.
-- tasks_status_check from 001_init.sql controls valid status values.
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check CHECK (
    status IN ('pending', 'running', 'waiting_for_approval', 'completed', 'failed', 'budget_exceeded', 'cancelled')
);
