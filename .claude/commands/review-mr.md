# Review GitLab Merge Request

Please review the GitLab merge request: $ARGUMENTS.

Follow these steps:

1. Use `glab mr view` to get the merge request details and metadata
2. Use `glab mr diff` to examine the code changes in the MR
3. Analyze the changes for:
   - Code quality and best practices
   - Potential bugs or security issues
   - Performance implications
   - Test coverage adequacy
   - Documentation updates needed
4. Check if the MR follows project conventions:
   - Coding standards and style guidelines
   - Commit message format
   - Branch naming conventions
5. Verify CI/CD pipeline status and test results
6. Search the codebase for related files that might be affected
7. Run relevant tests locally if needed to verify functionality
8. Check for breaking changes and backward compatibility
9. Draft detailed feedback including:
   - Inline code comments on specific lines
   - General review comments
   - Suggestions for improvements
   - Overall assessment and recommendation
10. Present the drafted review to you for confirmation before posting:
    - Show all proposed comments and their locations
    - Display the overall review summary
    - Ask for your approval before proceeding
11. After your confirmation, post the feedback using `glab mr note`
12. Update MR status only after your explicit approval:
    - Ask before approving with `glab mr approve`
    - Ask before requesting changes or adding labels
    - Confirm any assignee changes

Remember to use the GitLab CLI (`glab`) for all GitLab-related tasks and provide constructive, actionable feedback.
