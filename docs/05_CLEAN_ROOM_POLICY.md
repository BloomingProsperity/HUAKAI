This file is agent-facing and authoritative.

# Clean-Room Policy

Full feature parity or better remains mandatory; clean-room constraints change implementation method, not feature scope.

## Core Rule

Reference projects are empirical evidence, not source-code providers.

## Allowed From References

- Publicly observable behavior.
- Product workflows.
- Feature lists.
- Configuration concepts at the user-outcome level.
- Error and risk patterns.
- Public issue scenarios.
- Public documentation facts.
- Test ideas and acceptance expectations expressed independently.

## Prohibited From Non-MIT References

- Source code.
- Distinctive file structures.
- Comments.
- Database schemas.
- API implementation details.
- UI source.
- Unique layout or styling.
- Internal naming conventions.
- Algorithms expressed in code.
- Copied test code.

## Required Clean-Room Method

1. Record reference evidence as behavior or scenario.
2. Convert behavior into an independent requirement.
3. Design local architecture from first principles and project contracts.
4. Implement without viewing or copying protected implementation details.
5. Validate against local acceptance tests.

## License Risk Rule

License risk can change implementation method, isolation boundary, rollout strategy, or documentation requirements. It cannot delete a feature.
