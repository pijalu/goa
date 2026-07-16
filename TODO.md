# TODO

Research idea: Goa should have a dedicated plan tool to allow a planner mode to create/work/review a plan - it should simplify the management of key steps and allow an orchestrator to pass plan items to sub-agent for implementation instead of using markdown.

A plan should be easy to review/anotate by the user, like the diff tooling.

The concept: A special run mode for goa, where I would describe the work to be done - a planner would work out the plan with the tooling - when ready, it would show me the plan and I can navigate/annotate the plan for rework/changes. The planner doing update as required and starting another review.

If the plan is confirmed - the orchestrator can then start implementation, passing plan items/task to agents (the user should see all these happening !) and run review of the output/check it matches - the agent should be able to ask clarification back to the planner/orchestrator (and if needed, flow back to the user)

The run/sub-agent should allow very limited context model as the problem would be split in small chunks in the plan.

Review the idea/research/ask clarification as needed and write a spec on the topic
