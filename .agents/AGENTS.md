# Agent Instructions & Guidelines

This document outlines the operational guidelines, behavioral preferences, and engineering standards for all AI agents working on the **Limen** project.

## Behavior & Communication Style

- **Communication Tone**: Use formal, technical, and precise language. Avoid conversational fluff, pleasantries, or sycophancy.
- **Emoji Usage**: Strictly prohibited. No emojis in comments, commits, chat responses, or documentation.
- **Feedback & Criticism**: Be honest, direct, and critical. Proactively evaluate the user's instructions and designs, finding potential flaws and recommending optimizations or alternative approaches.
- When reporting information back to me, be extremely concise an sacrifice
  grammar for the sake of concision. Use bullet-points to express yourself when
  useful.

## Version Control & Commits

- **Automatic Commits**: At the end of coding or writing tasks, commit all changes.
- **Commit Message Style**: Use expressive, bullet-pointed, and simple descriptions of the changes. Keep it clear and straight to the point.

## Coding Standards & Architecture

- **Defensive Programming**: Write robust, fault-tolerant code. Validate inputs, handle boundary conditions, and design for resilience.
- **Modular Design**:
  - Functions must be short and focused on a single concern (Single Responsibility Principle).
  - Avoid excessively long lines of code (limit line lengths for maximum readability).
- **Naming Conventions**: Follow the project's standard naming conventions strictly. Always use descriptive and self-documenting variable, class, and function names.
- **Code Comments**:
  - Comment code only when necessary to explain non-obvious logic or architectural context.
  - Standardize annotation tags: Use `TODO`, `NOTE`, `BUG`, and `ISSUE` tags explicitly to call out pending tasks, critical notes, or bugs.

## Testing & Quality Assurance

- **Extensive Testing**: Write tests for all new features and modifications.
- **Meaningful Assertions**: "Tests that can't fail are not tests." Ensure tests validate real failure modes, edge cases, and correct state transitions under adversity.
