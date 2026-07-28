Feature: Task Lifecycle
  The Limen orchestration engine drives every task through a strict state
  machine across the full lifecycle: CREATED, CONTEXT_BUILDING,
  ROUTING_EVALUATION, WORKER_RUNNING, AWAITING_VALIDATION, APPROVED,
  COMMITTED or FAILED_ESCALATED.
  Each scenario asserts the states in the order the engine actually visited
  them, so a step that reads "and it reaches X" also asserts X came after the
  state named above it.

  Background:
    Given the orchestrator is initialized
    And the retrieval pipeline returns an empty manifest

  Scenario: Happy path reaches COMMITTED
    When a task is created
    Then it reaches CONTEXT_BUILDING
    And it reaches ROUTING_EVALUATION
    And the router decided PROCEED
    And it reaches WORKER_RUNNING
    And the worker produced a solution
    And it reaches AWAITING_VALIDATION
    And the validator approved the solution
    And it reaches APPROVED
    And it reaches COMMITTED
    And the task ends in COMMITTED
    And the canonical branch advanced

  Scenario: Zero coverage escalates at the router
    Given coverage hint is 0
    When a task is created
    Then it reaches CONTEXT_BUILDING
    And it reaches ROUTING_EVALUATION
    And the router decided ESCALATE
    And it reaches FAILED_ESCALATED
    And the task ends in FAILED_ESCALATED
    And the worker ran 0 times
    And the canonical branch did not advance
    And no final output was recorded

  Scenario: Validator rejection triggers revision
    Given the validator will reject the solution 1 times then approve
    When a task is created
    Then it reaches WORKER_RUNNING
    And it reaches AWAITING_VALIDATION
    And the validator rejected the solution
    And it reaches REVISION_REQUESTED
    And it returns to WORKER_RUNNING
    And it reaches AWAITING_VALIDATION
    And it reaches APPROVED
    And it reaches COMMITTED
    And the task ends in COMMITTED
    And the worker ran 2 times
    And the worker received the validator's feedback on its retry

  Scenario: Router expands the context before proceeding
    Given the router will expand 2 times before proceeding
    And each retrieval round returns richer context
    When a task is created
    Then it reaches CONTEXT_BUILDING
    And it reaches ROUTING_EVALUATION
    And it returns to CONTEXT_BUILDING
    And it reaches ROUTING_EVALUATION
    And it returns to CONTEXT_BUILDING
    And it reaches ROUTING_EVALUATION
    And the router decided PROCEED
    And it reaches WORKER_RUNNING
    And it reaches COMMITTED
    And the task ends in COMMITTED
    And the context was rebuilt 3 times
    And the router was consulted 3 times

  Scenario: Exhausting the expand budget escalates
    Given the router will decide EXPAND
    When a task is created
    Then it reaches CONTEXT_BUILDING
    And it reaches ROUTING_EVALUATION
    And it reaches FAILED_ESCALATED
    And the task ends in FAILED_ESCALATED
    And the context was rebuilt 6 times
    And the router was consulted 6 times
    And the worker ran 0 times
    And the canonical branch did not advance

  Scenario: Exhausting the retry budget escalates
    Given the validator will always reject the solution
    And the task allows 2 retries
    When a task is created
    Then it reaches WORKER_RUNNING
    And it reaches AWAITING_VALIDATION
    And it reaches REVISION_REQUESTED
    And it returns to WORKER_RUNNING
    And it reaches AWAITING_VALIDATION
    And it reaches REVISION_REQUESTED
    And it returns to WORKER_RUNNING
    And it reaches AWAITING_VALIDATION
    And it reaches FAILED_ESCALATED
    And the task ends in FAILED_ESCALATED
    And the retry budget is exhausted
    And the worker ran 3 times
    And the canonical branch did not advance
    And no final output was recorded
