package todo

// Deferred marks todo_list as an on-demand tool. Its schema is loaded through
// tool_search because todo tracking is only needed for task-oriented turns.
func (*TodoListTool) Deferred() bool { return true }
