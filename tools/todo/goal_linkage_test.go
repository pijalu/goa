package todo

import "testing"

func executeTodoRequest(t *testing.T, tool *TodoListTool, request string) string {
	t.Helper()
	output, err := tool.Execute(request)
	if err != nil {
		t.Fatal(err)
	}
	return output
}
