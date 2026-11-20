-- lineup writes this file each time it opens the Hub, so an edit here is lost.
-- To load it, add require("lineup") to init.lua.

-- A server older than API revision 17 can't ask, so it gets no actions.
if rex.ui == nil then
  return
end

-- rex.exec runs a command with the Rex server's PATH.
local LINEUP = "{{lineup}}"

local function lineup(args)
  local argv = { LINEUP }
  for _, a in ipairs(args) do
    argv[#argv + 1] = a
  end
  local r, err = rex.exec(argv, { timeout = 25 })
  if r == nil then
    error(err, 0)
  end
  if not r.ok then
    error(r.stderr ~= "" and r.stderr or ("lineup exited with " .. r.code), 0)
  end
  return r.stdout
end

local ICON = { build = "branch", review = "person" }

local function why(t)
  return t.note or t.stage
end

local function find(list, id)
  for _, x in ipairs(list) do
    if x.id == id then
      return x
    end
  end
end

local function pick(title, tasks)
  local items = {}
  for _, t in ipairs(tasks) do
    items[#items + 1] = { id = t.id, title = t.id .. "  " .. t.title, subtitle = why(t), icon = ICON[t.kind] }
  end
  local id, reason = rex.ui.pick{ title = title, placeholder = "Filter Tasks", items = items }
  return find(tasks, id), reason
end

rex.action{
  name = "lineup_next",
  title = "What Needs Me…",
  description = "Pick a lineup Task that needs you, then what to do about it.",
  category = "Lineup",
  keywords = "lineup needs you inbox decide",
  run = function(ctx, args)
    local tasks = rex.json.decode(lineup({ "decide", "--json" })).tasks
    if #tasks == 0 then
      rex.ui.pick{ title = "No Task needs you", items = {} }
      return { needs_you = 0 }
    end
    local title = #tasks == 1 and "1 Task needs you" or (#tasks .. " Tasks need you")
    local task, reason = pick(title, tasks)
    if task == nil then
      return { answered = false, reason = reason }
    end
    local choice = task.choices[1]
    if #task.choices > 1 or choice.id ~= "open" then
      local items = {}
      for _, c in ipairs(task.choices) do
        items[#items + 1] = { id = c.id, title = c.title }
      end
      local id
      id, reason = rex.ui.pick{ title = task.id .. ": " .. why(task), items = items }
      choice = find(task.choices, id)
      if choice == nil then
        return { task = task.id, answered = false, reason = reason }
      end
    end
    local argv = { "decide", task.id, choice.id }
    if choice.takes_text then
      local text
      text, reason = rex.ui.input{ title = task.id .. ": " .. why(task), placeholder = "Your words for the Worker", confirm = "Send to " .. task.id }
      if text == nil then
        return { task = task.id, choice = choice.id, answered = false, reason = reason }
      end
      argv[#argv + 1] = text
    end
    return { task = task.id, choice = choice.id, out = lineup(argv) }
  end,
}

rex.action{
  name = "lineup_steer",
  title = "Tell a Worker…",
  description = "Type your words into a lineup Worker.",
  category = "Lineup",
  keywords = "lineup steer worker say",
  run = function(ctx, args)
    local tasks = {}
    for _, t in ipairs(rex.json.decode(lineup({ "ls", "--json" })).tasks) do
      if t.live then
        tasks[#tasks + 1] = t
      end
    end
    if #tasks == 0 then
      rex.ui.pick{ title = "No Worker runs", items = {} }
      return { workers = 0 }
    end
    local task, reason = pick("Tell which Worker?", tasks)
    if task == nil then
      return { answered = false, reason = reason }
    end
    local text
    text, reason = rex.ui.input{ title = "Tell " .. task.id, placeholder = "Your words for the Worker", confirm = "Send to " .. task.id }
    if text == nil then
      return { task = task.id, answered = false, reason = reason }
    end
    return { task = task.id, out = lineup({ "steer", task.id, text }) }
  end,
}
