# Progress

## Completed
- [x] Step 1: Project scaffold
  - Encore CLI installed (brew, v1.57.1)
  - `encore app create go-fees-api`
  - `bill/migrations/` created
  - Git history restored, pushed to `origin main`
  - Commit: `chore: scaffold Encore project`

## In Progress
- [ ] Step 2: DB migration (`bill/migrations/1_create_tables.up.sql`)
- [ ] Step 3: Service struct + Temporal worker (`bill/service.go`)
- [ ] Step 4: Temporal activities (`bill/activities.go`)
- [ ] Step 5: Temporal workflow (`bill/workflow.go`)
- [ ] Step 6: API endpoints (`bill/bill.go`)
- [ ] Verify: `encore check` + `encore run`
