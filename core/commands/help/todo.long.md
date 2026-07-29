Manage the active goal's todo list.

Usage:
  /todo                     List todos (same as /todo:list)
  /todo:list                Numbered todo list with status markers
  /todo:add:<title>         Add a todo
  /todo:add                 Add a todo (asks the title on the input line)
  /todo:edit:<n>            Edit todo n — input line opens prefilled with its title
  /todo:edit:<n>:<title>    Rename todo n directly
  /todo:done:<n>            Mark todo n done
  /todo:undone:<n>          Mark todo n pending again
  /todo:delete:<n>          Delete todo n (alias: rm)

Positions are 1-based, as printed by /todo:list. Todos belong to the active
goal: they are created with it and die with it — there is no todo list
without an active goal (start one with /goal:new:<objective>).

The footer shows one ⬩ per pending todo next to the mode (up to 3, then
+x); every /todo change refreshes it immediately.
