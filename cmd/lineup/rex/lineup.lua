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

local function taskItem(t)
  return { id = t.id, title = t.id .. "  " .. t.title, subtitle = why(t), icon = ICON[t.kind] }
end

local function pick(title, tasks)
  local items = {}
  for _, t in ipairs(tasks) do
    items[#items + 1] = taskItem(t)
  end
  local id, reason = rex.ui.pick{ title = title, placeholder = "Filter Tasks", items = items }
  return find(tasks, id), reason
end

local function board()
  return rex.json.decode(lineup({ "ls", "--json" }))
end

local function tasksWhere(v, keep)
  local out = {}
  for _, t in ipairs(v.tasks) do
    if keep(t) then
      out[#out + 1] = t
    end
  end
  return out
end

-- pickTask asks for one Task of the Board that keep accepts. With none, it
-- says so in an empty list.
local function pickTask(title, none, keep)
  local tasks = tasksWhere(board(), keep)
  if #tasks == 0 then
    rex.ui.pick{ title = none, items = {} }
    return nil, "none"
  end
  return pick(title, tasks)
end

local function onBoard(t)
  return t.stage_key ~= "done" or t.tab
end

local function notDone(t)
  return t.stage_key ~= "done"
end

local function started(t)
  return t.stage_key ~= "done"
end

-- add runs lineup add with args, and the add starts the Task. If the WIP cap
-- is full, it asks to start the Task once over the cap. name names the Task in
-- the choices, and leave is the choice that starts nothing.
local function add(args, name, leave)
  local cmd = { "add" }
  for _, a in ipairs(args) do
    cmd[#cmd + 1] = a
  end
  local ok, out = pcall(lineup, cmd)
  if ok then
    return out
  end
  if not tostring(out):find("wip cap full", 1, true) then
    error(out, 0)
  end
  local yes, reason = rex.ui.pick{
    title = "The WIP cap is full",
    items = {
      { id = "over", title = "Start " .. name .. " over the cap" },
      { id = "leave", title = leave },
    },
  }
  if yes ~= "over" then
    return nil, reason or "not started"
  end
  table.insert(cmd, 2, "--over-cap")
  return lineup(cmd)
end

-- The agent Stages of each Kind, the ones that Run a Stage Again can run.
-- rexactions_test.go checks this table against the lifecycle package.
local AGENT_STAGES = {
  build = {
    { id = "working", title = "Working" },
    { id = "ci_debug", title = "CI debug" },
    { id = "responding", title = "Responding" },
  },
  review = {
    { id = "reviewing", title = "Reviewing" },
  },
}

local MODELS = { "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-1" }

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
    local task, reason = pickTask("Tell which Worker?", "No Worker runs", function(t) return t.live end)
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

rex.action{
  name = "lineup_goto",
  title = "Go to a Task…",
  description = "Focus a lineup Task's tab, or restore its Worker.",
  category = "Lineup",
  keywords = "lineup open focus jump switch tab",
  run = function(ctx, args)
    local task, reason = pickTask("Go to which Task?", "No Task is on the Board", onBoard)
    if task == nil then
      return { answered = false, reason = reason }
    end
    return { task = task.id, out = lineup({ "open", task.id }) }
  end,
}

rex.action{
  name = "lineup_start",
  title = "Start a Task…",
  description = "Start a lineup Task for an Issue.",
  category = "Lineup",
  keywords = "lineup start issue begin add",
  run = function(ctx, args)
    local issue, reason = rex.ui.input{ title = "Start which Issue?", placeholder = "ENG-614, owner/repo#12, local-7, or hod-3", confirm = "Start the Task" }
    if issue == nil then
      return { answered = false, reason = reason }
    end
    issue = issue:match("^%s*(.-)%s*$")
    local out
    out, reason = add({ issue }, issue, "Do not start " .. issue)
    return { task = issue, started = out ~= nil, out = out, reason = reason }
  end,
}

rex.action{
  name = "lineup_adhoc",
  title = "Start an Ad Hoc Task…",
  description = "Give a Worker a job in a Repo, with no Issue.",
  category = "Lineup",
  keywords = "lineup ad hoc adhoc new task worker job",
  run = function(ctx, args)
    local repos, seen = {}, {}
    for _, t in ipairs(board().tasks) do
      if t.kind == "build" and t.repo ~= "" and not seen[t.repo] then
        seen[t.repo] = true
        repos[#repos + 1] = { id = t.repo, icon = "folder" }
      end
    end
    local repo, reason = rex.ui.pick{
      title = "In which Repo?",
      placeholder = "Filter Repos, or type a Repo name or path",
      items = repos,
      allow_other = true,
    }
    if repo == nil then
      return { answered = false, reason = reason }
    end
    if type(repo) == "table" then
      repo = repo.text
    end
    local text
    text, reason = rex.ui.input{ title = "What must the Worker do in " .. repo .. "?", placeholder = "The job, in your words", confirm = "Start the Task" }
    if text == nil then
      return { repo = repo, answered = false, reason = reason }
    end
    local out
    out, reason = add({ "--repo", repo, text }, "the Task", "Do not start it")
    return { task = out and out:match("^(%S+)"), started = out ~= nil, out = out, reason = reason }
  end,
}

rex.action{
  name = "lineup_review",
  title = "Review a PR…",
  description = "Start a lineup review of someone else's PR.",
  category = "Lineup",
  keywords = "lineup review pr pull request",
  run = function(ctx, args)
    local url, reason = rex.ui.input{ title = "Review which PR?", placeholder = "https://github.com/owner/repo/pull/12", confirm = "Start the review" }
    if url == nil then
      return { answered = false, reason = reason }
    end
    return { pr = url, out = lineup({ "review", url }) }
  end,
}

rex.action{
  name = "lineup_close",
  title = "Close a Task…",
  description = "End a lineup Task, and close its tab and worktree.",
  category = "Lineup",
  keywords = "lineup close end stop cancel task",
  run = function(ctx, args)
    local task, reason = pickTask("Close which Task?", "No Task is open", onBoard)
    if task == nil then
      return { answered = false, reason = reason }
    end
    local yes
    yes, reason = rex.ui.pick{
      title = "Close " .. task.id .. "?",
      items = {
        { id = "close", title = "Close " .. task.id, subtitle = "End the Task, and close its tab and worktree" },
        { id = "keep", title = "Keep " .. task.id },
      },
    }
    if yes ~= "close" then
      return { task = task.id, closed = false, reason = reason or "kept" }
    end
    return { task = task.id, closed = true, out = lineup({ "close", task.id }) }
  end,
}

rex.action{
  name = "lineup_archive",
  title = "Archive Done Tasks…",
  description = "Close the tabs and worktrees of Done lineup Tasks, and hide them from the Board.",
  category = "Lineup",
  keywords = "lineup archive done clean hide",
  run = function(ctx, args)
    local items = {}
    for _, t in ipairs(tasksWhere(board(), function(t) return t.stage_key == "done" end)) do
      items[#items + 1] = taskItem(t)
    end
    if #items == 0 then
      rex.ui.pick{ title = "No Task is Done", items = {} }
      return { archived = 0 }
    end
    local ids, reason = rex.ui.pick{ title = "Archive which Tasks?", placeholder = "Filter Done Tasks", items = items, multiple = true, confirm = "Archive" }
    if ids == nil then
      return { answered = false, reason = reason }
    end
    for _, id in ipairs(ids) do
      lineup({ "archive", id })
    end
    return { archived = #ids, tasks = ids }
  end,
}

rex.action{
  name = "lineup_rerun",
  title = "Run a Stage Again…",
  description = "Type a Stage's prompt into a lineup Worker again.",
  category = "Lineup",
  keywords = "lineup rerun retry again stage prompt",
  run = function(ctx, args)
    local task, reason = pickTask("Run a Stage of which Task?", "No Task has started", started)
    if task == nil then
      return { answered = false, reason = reason }
    end
    local items = {}
    for _, st in ipairs(AGENT_STAGES[task.kind] or {}) do
      local item = { id = st.id, title = st.title }
      if st.id == task.stage_key then
        item.detail = "now"
        table.insert(items, 1, item)
      else
        items[#items + 1] = item
      end
    end
    local stage
    stage, reason = rex.ui.pick{ title = "Run which Stage of " .. task.id .. "?", items = items }
    if stage == nil then
      return { task = task.id, answered = false, reason = reason }
    end
    return { task = task.id, stage = stage, out = lineup({ "rerun", task.id, stage }) }
  end,
}

rex.action{
  name = "lineup_cap",
  title = "Set the WIP Cap…",
  description = "Set how many lineup build Tasks can run at once.",
  category = "Lineup",
  keywords = "lineup wip cap limit concurrency",
  run = function(ctx, args)
    local v = board()
    local text, reason = rex.ui.input{
      title = "WIP cap: " .. v.started .. " Tasks started",
      placeholder = "A number of 1 or more",
      value = tostring(v.cap),
      confirm = "Set the cap",
    }
    if text == nil then
      return { answered = false, reason = reason }
    end
    local n = tonumber(text)
    if n == nil or n < 1 or n ~= math.floor(n) then
      error("the cap must be a whole number of 1 or more, not " .. text, 0)
    end
    return { cap = n, out = lineup({ "cap", tostring(n) }) }
  end,
}

rex.action{
  name = "lineup_reviews_pause",
  title = "Pause Reviews",
  description = "Stop every lineup review queue from starting reviews.",
  category = "Lineup",
  keywords = "lineup reviews pause stop hold queue",
  run = function(ctx, args)
    return { out = lineup({ "reviews", "pause" }) }
  end,
}

rex.action{
  name = "lineup_reviews_resume",
  title = "Resume Reviews",
  description = "Let the lineup review queues start reviews again.",
  category = "Lineup",
  keywords = "lineup reviews resume start unpause queue",
  run = function(ctx, args)
    return { out = lineup({ "reviews", "resume" }) }
  end,
}

rex.action{
  name = "lineup_model",
  title = "Set a Task's Model…",
  description = "Pick the model of a lineup Task's next Worker.",
  category = "Lineup",
  keywords = "lineup model claude opus sonnet haiku set",
  run = function(ctx, args)
    local task, reason = pickTask("Set the model of which Task?", "No Task is open", notDone)
    if task == nil then
      return { answered = false, reason = reason }
    end
    local model
    model, reason = rex.ui.pick{
      title = "Model for " .. task.id .. "'s next Worker",
      placeholder = "Filter models, or type a model ID",
      items = MODELS,
      allow_other = true,
    }
    if model == nil then
      return { task = task.id, answered = false, reason = reason }
    end
    if type(model) == "table" then
      model = model.text
    end
    return { task = task.id, model = model, out = lineup({ "set", task.id, "model=" .. model }) }
  end,
}
