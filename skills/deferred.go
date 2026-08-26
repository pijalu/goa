package skills

// Deferred marks run_skill as an on-demand tool. The model can discover the
// available skill catalog in the prompt and loads this execution schema only
// when it needs to invoke a skill.
func (*SkillRunnerTool) Deferred() bool { return true }
