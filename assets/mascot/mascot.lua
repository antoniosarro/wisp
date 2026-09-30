-- Generates mascot.aseprite next to this script: 64x64 Indexed on the
-- wisp.gpl palette, layers body/face/fx, one 6-frame tag per state, 200 ms
-- per frame. It overwrites the file, including any edits made in Aseprite:
--   aseprite -b --script assets/mascot/mascot.lua
local DIR = debug.getinfo(1, "S").source:match("^@(.*)/[^/]*$") or "."
local W = 64
local pc = app.pixelColor
local C = {
  o = pc.rgba(21, 19, 31), w = pc.rgba(244, 242, 255), s = pc.rgba(201, 196, 232),
  d = pc.rgba(142, 139, 176), M = pc.rgba(215, 65, 245), c = pc.rgba(80, 220, 235),
  L = pc.rgba(162, 155, 245), m = pc.rgba(94, 230, 176), r = pc.rgba(255, 92, 138),
  a = pc.rgba(255, 184, 108),
}

local spr = Sprite(W, W, ColorMode.RGB)
spr:setPalette(Palette { fromFile = DIR .. "/wisp.gpl" })
local body = spr.layers[1]; body.name = "body"
local face = spr:newLayer(); face.name = "face"
local fx = spr:newLayer(); fx.name = "fx"

-- ghost reports whether point (x, y) is inside the ghost silhouette:
-- a round head plus a tail curling down and to the left.
local tail = {}
for i = 0, 60 do
  local t = i / 60
  local u = 1 - t
  tail[#tail + 1] = {
    u^3 * 32 + 3 * u^2 * t * 31 + 3 * u * t^2 * 20 + t^3 * 10,
    u^3 * 30 + 3 * u^2 * t * 48 + 3 * u * t^2 * 54 + t^3 * 47,
    13 * u + 1.8 * t,
  }
end
local function ghost(x, y)
  if (x - 30) ^ 2 + (y - 24) ^ 2 <= 15 ^ 2 then return true end
  for _, p in ipairs(tail) do
    if (x - p[1]) ^ 2 + (y - p[2]) ^ 2 <= p[3] ^ 2 then return true end
  end
  return false
end

-- Pixel helpers.
local function new() return Image(W, W, ColorMode.RGB) end
local function opaque(img, x, y)
  return x >= 0 and x < W and y >= 0 and y < W and pc.rgbaA(img:getPixel(x, y)) > 0
end
local function put(img, x, y, col)
  if x >= 0 and x < W and y >= 0 and y < W then img:drawPixel(x, y, C[col]) end
end
local function each(img, test, col)
  for y = 0, W - 1 do
    for x = 0, W - 1 do
      if test(x, y) then img:drawPixel(x, y, C[col]) end
    end
  end
end
local function disc(img, cx, cy, r, col)
  each(img, function(x, y) return (x - cx) ^ 2 + (y - cy) ^ 2 <= r * r end, col)
end
local function line(img, x0, y0, x1, y1, col, th)
  th = th or 1
  local n = math.max(math.abs(x1 - x0), math.abs(y1 - y0)) * 2
  for i = 0, n do
    local x = math.floor(x0 + (x1 - x0) * i / n + 0.5)
    local y = math.floor(y0 + (y1 - y0) * i / n + 0.5)
    for dx = 0, th - 1 do for dy = 0, th - 1 do put(img, x + dx, y + dy, col) end end
  end
end
local function stamp(img, rows, x, y)
  for j, row in ipairs(rows) do
    for i = 1, #row do
      local ch = row:sub(i, i)
      if ch ~= "." then put(img, x + i - 1, y + j - 1, ch) end
    end
  end
end
-- outline adds a 1 px dark outline around everything opaque in img.
local function outline(img)
  local out = img:clone()
  for y = 0, W - 1 do
    for x = 0, W - 1 do
      if not opaque(img, x, y) and (opaque(img, x + 1, y) or opaque(img, x - 1, y)
          or opaque(img, x, y + 1) or opaque(img, x, y - 1)) then
        out:drawPixel(x, y, C.o)
      end
    end
  end
  return out
end
-- silhouette draws the ghost scaled by s at (ox, oy) with a shade band on
-- the lower right that dithers into the body color.
local function silhouette(img, s, ox, oy, main, shade, band)
  local function inside(x, y) return ghost((x - ox) / s, (y - oy) / s) end
  each(img, function(x, y)
    if not inside(x, y) then return false end
    for k = 1, band do
      if not inside(x + k, y + k) or not inside(x + k + 1, y) or not inside(x, y + k + 1) then
        return k < band or (x + y) % 2 == 0
      end
    end
    return false
  end, shade)
  each(img, function(x, y)
    return inside(x, y) and not opaque(img, x, y)
  end, main)
  return outline(img)
end

local bodyImg = silhouette(new(), 1, 0, 0, "w", "s", 5)

-- Faces, in body coordinates.
local EYE = { ".o.", "oow", "ooo", "ooo", "ooo", ".o." }
local WIDE = { ".o.", "oow", "oow", "ooo", "ooo", "ooo", ".o." }
local EYES = {
  open  = function(i) stamp(i, EYE, 33, 18); stamp(i, EYE, 40, 18) end,
  up    = function(i) stamp(i, EYE, 33, 16); stamp(i, EYE, 40, 16) end,
  down  = function(i) stamp(i, EYE, 34, 20); stamp(i, EYE, 41, 20) end,
  wide  = function(i) stamp(i, WIDE, 33, 15); stamp(i, WIDE, 40, 15) end,
  blink = function(i)
    stamp(i, { "o...o", ".ooo." }, 32, 21); stamp(i, { "o...o", ".ooo." }, 39, 21)
  end,
  happy = function(i)
    stamp(i, { ".ooo.", "o...o" }, 32, 19); stamp(i, { ".ooo.", "o...o" }, 39, 19)
  end,
}
local MOUTH = {
  none  = {},
  flat  = { "ooo" },
  smile = { "o...o", ".ooo." },
  o     = { ".oo.", "oooo", "oooo", ".oo." },
  big   = { ".ooo.", "ooooo", "ooooo", ".orro" },
}
local function cheeks(i) stamp(i, { "rr" }, 31, 26); stamp(i, { "rr" }, 43, 26) end

-- Effects.
local function dots(a, b, c)
  return function(img)
    if a then disc(img, 44, 8, 1.2, a) end
    if b then disc(img, 50, 5, 2, b) end
    if c then disc(img, 57, 4, 2.8, c) end
  end
end
local function quill(dx, n)
  return function(img)
    local x0, y0, x1, y1 = 46 + dx, 53, 59 + dx, 33
    local lx, ly = x1 - x0, y1 - y0
    local len2 = lx * lx + ly * ly
    each(img, function(x, y)
      local u = ((x - x0) * lx + (y - y0) * ly) / len2
      if u < 0.18 or u > 1 then return false end
      local d = math.abs((x - x0) * ly - (y - y0) * lx) / math.sqrt(len2)
      return d <= 3.6 * math.sin(math.pi * (u - 0.18) / 0.82) + 0.4
    end, "c")
    line(img, x0, y0, x1 - 1, y1 + 2, "w")
    for k = 0, 2 do line(img, x0 + 4 + 2 * k, y0 - 9 - 3 * k, x0 + 7 + 2 * k, y0 - 9 - 3 * k, "s") end
    put(img, x0, y0, "d"); put(img, x0 - 1, y0 + 1, "o")
    for i = 0, n - 1 do
      put(img, 44 + i, 59 + math.floor(1.5 * math.sin(i * 0.9) + 0.5), "m")
    end
  end
end
local function star(img, cx, cy)
  line(img, cx - 3, cy, cx + 3, cy, "a"); line(img, cx, cy - 3, cx, cy + 3, "a")
  put(img, cx - 1, cy - 1, "a"); put(img, cx + 1, cy + 1, "a")
  put(img, cx + 1, cy - 1, "a"); put(img, cx - 1, cy + 1, "a"); put(img, cx, cy, "w")
end
local function wrench(deg, spark)
  return function(img)
    local px, py, t = 48, 58, math.rad(deg)
    local ct, st = math.cos(t), math.sin(t)
    each(img, function(x, y)
      local u = (x - px) * ct + (y - py) * st
      local v = -(x - px) * st + (y - py) * ct
      if u >= 0 and u <= 13 and math.abs(v) <= 1.6 then return true end
      return (u - 16) ^ 2 + v * v <= 4.8 ^ 2 and (u - 19.5) ^ 2 + v * v > 2.6 ^ 2
    end, "a")
    each(img, function(x, y)
      local u = (x - px) * ct + (y - py) * st
      local v = -(x - px) * st + (y - py) * ct
      return u >= 1 and u <= 11 and v > 0.6 and v <= 1.6
    end, "w")
    if spark then star(img, 55, 36) end
  end
end
local function ring(cx, cy, r)
  return function(img)
    each(img, function(x, y)
      local d = math.sqrt((x - cx) ^ 2 + (y - cy) ^ 2)
      return d <= r and d > r - 1.6
    end, "L")
  end
end
local function mini(ox, oy)
  return function(img)
    local g = silhouette(new(), 0.34, ox, oy, "L", "d", 1)
    img:drawImage(g, Point(0, 0))
    local ex, ey = ox + math.floor(0.34 * 34 + 0.5), oy + math.floor(0.34 * 20 + 0.5)
    stamp(img, { "o.o", "o.o" }, ex, ey)
  end
end
local Q = { ".aaaa.", "aa..aa", "aa..aa", "...aa.", "..aa..", "..aa..", "......", "..aa..", "..aa.." }
local BANG = { "aa", "aa", "aa", "aa", "aa", "aa", "..", "aa", "aa" }
local function glyph(rows, x, y) return function(img) stamp(img, rows, x, y) end end

-- Each state: 6 frames of { body dy, eyes, mouth, fx, cheeks, body dx,
-- face extra, fx unoutlined }. fx may return a function that draws thin
-- details after the outline, which would otherwise swallow them.
local S = {
  idle = {
    { 0, "open", "none", nil, true }, { 0, "open", "none", nil, true }, { -2, "open", "none", nil, true },
    { -2, "blink", "none", nil, true }, { -2, "open", "none", nil, true }, { 0, "open", "none", nil, true },
  },
  thinking = {
    { 0, "up", "flat" }, { 0, "up", "flat", dots("L") }, { 0, "up", "flat", dots("L", "L") },
    { -2, "up", "flat", dots("L", "L", "L") }, { -2, "up", "flat", dots(nil, "d", "L") },
    { 0, "up", "flat", dots(nil, nil, "d") },
  },
  writing = {
    { 0, "down", "smile", quill(0, 0), true }, { 0, "down", "smile", quill(1, 3), true },
    { 0, "down", "smile", quill(0, 7), true }, { 0, "down", "smile", quill(1, 10), true },
    { 0, "down", "smile", quill(0, 14), true }, { 0, "down", "smile", quill(1, 18), true },
  },
  tool = {
    { 0, "open", "flat", wrench(-50) }, { 0, "open", "flat", wrench(-70) },
    { 0, "open", "flat", wrench(-50, true) }, { 0, "open", "flat", wrench(-50) },
    { 0, "open", "flat", wrench(-70) }, { 0, "open", "flat", wrench(-50, true) },
  },
  agent = {
    { 0, "open", "o" }, { 0, "open", "o", ring(51, 28, 3) }, { -2, "open", "o", ring(53, 25, 5.5) },
    { 0, "open", "smile", mini(46, 18), true }, { 0, "open", "smile", mini(47, 10), true },
    { 0, "open", "smile", mini(47, 2), true },
  },
  waiting = {
    { 0, "up", "flat", glyph(Q, 49, 1) }, { 0, "up", "flat", glyph(Q, 49, 2) },
    { 0, "up", "flat", glyph(Q, 49, 1) }, { -2, "wide", "o", glyph(BANG, 51, 0) },
    { 0, "up", "flat", glyph(Q, 49, 2) }, { 0, "up", "flat", glyph(Q, 49, 1) },
  },
  sleeping = {},
}

-- drifter lets go of a glyph every 3 frames and moves it through path's
-- six { x, y, rows } steps, so two are always in the air and the loop is
-- seamless.
local function drifter(path)
  return function(k)
    return function(img)
      for _, age in ipairs { k % 6, (k + 3) % 6 } do
        local p = path[age + 1]
        stamp(img, p[3], p[1], p[2])
      end
    end
  end
end
local function tint(rows, from, to)
  local out = {}
  for i, r in ipairs(rows) do out[i] = r:gsub(from, to) end
  return out
end

local ZS = { "LLLLL", "...LL", "..LL.", ".LL..", "LLLLL" }
local ZB = { "LLLLLL", "LLLLLL", "...LL.", "..LL..", ".LL...", "LLLLLL", "LLLLLL" }
local zs = drifter { { 47, 17, ZS }, { 48, 13, ZS }, { 50, 9, ZS }, { 51, 5, ZB }, { 54, 2, ZB }, { 57, 0, tint(ZB, "L", "d") } }
for k, dy in ipairs { 0, 0, 2, 2, 2, 0 } do
  S.sleeping[k] = { dy, "blink", "flat", zs(k - 1), true }
end

-- Easter eggs, played when a message mentions them (mascotEggs in
-- internal/tui/mascot.go).

-- waving: a stubby hand at the right side.
local function hand(x, y, swish)
  return function(img)
    disc(img, x, y, 3.8, "w")
    for _, d in ipairs { { 2, 2 }, { 3, 1 }, { 1, 3 }, { 3, 0 }, { 0, 3 } } do put(img, x + d[1], y + d[2], "s") end
    if swish then
      return function(o) stamp(o, { "..L", ".L.", "L..", "L..", ".L." }, x + 6, y - 4) end
    end
  end
end
S.waving = {}
for k, h in ipairs { { 49, 36 }, { 51, 31, true }, { 50, 26 }, { 51, 31, true }, { 50, 26 }, { 51, 31, true } } do
  S.waving[k] = { 0, "happy", "smile", hand(h[1], h[2], h[3]), true }
end

-- loved: hearts float up.
local HS = { "r.r", "rrr", ".r." }
local HB = { ".rr.rr.", "rwrrrrr", "rrrrrrr", ".rrrrr.", "..rrr..", "...r..." }
local hearts = drifter { { 46, 20, HS }, { 47, 16, HS }, { 48, 11, HB }, { 50, 7, HB }, { 52, 3, HB }, { 55, 0, HB } }
S.loved = {}
for k, dy in ipairs { 0, 0, -2, -2, 0, 0 } do
  S.loved[k] = { dy, "happy", "smile", hearts(k - 1), true }
end

-- spooky: "boo!" makes it jump and shake.
local function boo(bang)
  return function(img)
    if bang then stamp(img, BANG, 52, 0) end
    return function(o)
      stamp(o, { ".L", "L.", "L.", ".L" }, 48, 16)
      stamp(o, { ".L", "L.", "L.", "L.", ".L" }, 51, 13)
    end
  end
end
S.spooky = {
  { 0, "open", "o" }, { -4, "wide", "big", boo(true), false, 0 }, { -5, "wide", "big", boo(), false, 1 },
  { -4, "wide", "big", boo(true), false, -1 }, { -5, "wide", "big", boo(), false, 1 }, { -2, "open", "o" },
}

-- partying: a party hat and falling confetti.
local function hat(img)
  local h = new()
  local ax, ay, l, r, base = 34, 0, 24, 37, 11
  each(h, function(x, y)
    if y < ay or y > base then return false end
    local t = (y - ay) / (base - ay)
    return x >= ax + (l - ax) * t and x <= ax + (r - ax) * t
  end, "M")
  each(h, function(x, y) return opaque(h, x, y) and (x + y) % 6 < 2 end, "a")
  disc(h, ax, ay + 1, 1.6, "c")
  img:drawImage(outline(h), Point(0, 0))
end
local CONFETTI = {
  { 4, 3, "c" }, { 12, 40, "a" }, { 19, 22, "r" }, { 27, 51, "m" }, { 40, 9, "M" }, { 45, 33, "c" },
  { 50, 18, "a" }, { 55, 46, "L" }, { 59, 5, "r" }, { 8, 28, "m" }, { 34, 56, "a" }, { 62, 30, "M" },
}
local function confetti(k)
  return function(img)
    for i, c in ipairs(CONFETTI) do
      local x, y = c[1], (c[2] + 10 * k) % 60
      put(img, x, y, c[3])
      if (i + k) % 2 == 0 then put(img, x + 1, y, c[3]) else put(img, x, y + 1, c[3]) end
    end
  end
end
S.partying = {}
for k, dy in ipairs { 0, -3, 0, -3, 0, -3 } do
  S.partying[k] = { dy, "happy", k % 2 == 0 and "o" or "smile", confetti(k - 1), true, 0, hat, true }
end

-- dancing: sways to floating notes.
local NOTE = { "..ccc", "..c.c", "..c..", "ccc..", "ccc.." }
local notes = drifter { { 47, 20, NOTE }, { 49, 15, NOTE }, { 50, 11, NOTE }, { 52, 7, NOTE }, { 54, 3, NOTE }, { 56, 0, tint(NOTE, "c", "L") } }
S.dancing = {}
for k, dx in ipairs { -2, -1, 1, 2, 1, -1 } do
  S.dancing[k] = { k % 2 == 0 and -2 or 0, "happy", k % 3 == 0 and "o" or "smile", notes(k - 1), true, dx }
end

-- sipping: a steaming mug, raised for a sip.
local function mug(k, dy)
  return function(img)
    each(img, function(x, y) return x >= 46 and x <= 52 and y >= 27 + dy and y <= 34 + dy end, "w")
    line(img, 46, 27 + dy, 52, 27 + dy, "a")
    line(img, 52, 28 + dy, 52, 34 + dy, "s")
    stamp(img, { "ww.", "..w", "..w", "ww." }, 53, 29 + dy)
    return function(o)
      for i = 0, 5 do
        put(o, 48 + math.floor(math.sin((i + k) * 1.3) + 0.5), 25 + dy - i, "d")
        put(o, 51 + math.floor(math.sin((i + k + 2) * 1.3) + 0.5), 24 + dy - i, "d")
      end
    end
  end
end
S.sipping = {}
for k = 1, 6 do
  local sip = k == 3 or k == 4
  S.sipping[k] = { 0, sip and "blink" or "open", "smile", mug(k, sip and -2 or 0), true }
end

local order = {
  "idle", "thinking", "writing", "tool", "agent", "waiting", "sleeping",
  "waving", "loved", "spooky", "partying", "dancing", "sipping",
}
for _ = 2, #order * 6 do spr:newEmptyFrame() end
for f = 1, #spr.frames do spr.frames[f].duration = 0.2 end

for si, name in ipairs(order) do
  local first = (si - 1) * 6 + 1
  for k, st in ipairs(S[name]) do
    local f = spr.frames[first + k - 1]
    local dy, eyes, mouth, drawfx, blush, dx, extra, raw = table.unpack(st)
    local at = Point(dx or 0, dy)
    spr:newCel(body, f, bodyImg, at)
    local fi = new()
    EYES[eyes](fi)
    stamp(fi, MOUTH[mouth], mouth == "flat" and 37 or 36, 28)
    if blush then cheeks(fi) end
    if extra then extra(fi) end
    spr:newCel(face, f, fi, at)
    if drawfx then
      local xi = new()
      local over = drawfx(xi)
      if not raw then xi = outline(xi) end
      if over then over(xi) end
      spr:newCel(fx, f, xi, Point(0, 0))
    end
  end
  local tag = spr:newTag(first, first + 5)
  tag.name = name
  tag.aniDir = AniDir.FORWARD
end

-- Indexed on wisp.gpl, whose entry order is each color's role (mascotANSI
-- in internal/tui/termcolors.go), plus a transparent entry appended after it.
local pal = Palette { fromFile = DIR .. "/wisp.gpl" }
local clear = #pal
pal:resize(clear + 1)
pal:setColor(clear, Color { r = 0, g = 0, b = 0, a = 0 })
spr:setPalette(pal)
app.command.ChangePixelFormat { format = "indexed", dithering = "none" }
spr.transparentColor = clear

spr:saveAs(DIR .. "/mascot.aseprite")
print("frames", #spr.frames, "tags", #spr.tags, "size", spr.width, spr.height,
  "mode", spr.colorMode == ColorMode.INDEXED and "indexed" or "rgb", "transparent", spr.transparentColor)
