---@meta

-- Type stub for cadpad's low-level `sdf` module. See
-- internal/cadpad/lua/bind.go for the authoritative bindings. Loaded by
-- lua-language-server as a workspace library so generated Lua gets diagnostics
-- and completion for the sdf API. The drift test enforces that every binding
-- name in bind.go appears here. Scripts use either the global `sdf` or
-- `local sdf = require("sdf")`; the global form is validated against this stub.

---@class SDF3
local SDF3 = {}

---Boolean union with one or more solids.
---@param ... SDF3
---@return SDF3
function SDF3:union(...) end

---Boolean difference (self minus other).
---@param other SDF3
---@return SDF3
function SDF3:diff(other) end

---Boolean intersection.
---@param other SDF3
---@return SDF3
function SDF3:intersect(other) end

---Boolean exclusive-or.
---@param other SDF3
---@return SDF3
function SDF3:xor(other) end

---Set the smooth blend radius applied to the NEXT boolean op.
---@param blendRadius number
---@return SDF3
function SDF3:k(blendRadius) end

---Translate by (x, y, z).
---@param x number
---@param y number
---@param z number
---@return SDF3
function SDF3:translate(x, y, z) end

---Uniform scale.
---@param factor number
---@return SDF3
function SDF3:scale(factor) end

---Rotate by rad radians about axis (ax, ay, az).
---@param rad number
---@param ax number
---@param ay number
---@param az number
---@return SDF3
function SDF3:rotate(rad, ax, ay, az) end

---Rotate about the X axis.
---@param rad number
---@return SDF3
function SDF3:rotate_x(rad) end

---Rotate about the Y axis.
---@param rad number
---@return SDF3
function SDF3:rotate_y(rad) end

---Rotate about the Z axis.
---@param rad number
---@return SDF3
function SDF3:rotate_z(rad) end

---Offset the surface by d.
---@param d number
---@return SDF3
function SDF3:offset(d) end

---Hollow the solid to wall thickness t.
---@param t number
---@return SDF3
function SDF3:shell(t) end

---Elongate along each axis.
---@param x number
---@param y number
---@param z number
---@return SDF3
function SDF3:elongate(x, y, z) end

---@class sdflib
sdf = {}

---@param radius number
---@return SDF3
function sdf.sphere(radius) end

---@param x number
---@param y number
---@param z number
---@param round number
---@return SDF3
function sdf.box(x, y, z, round) end

---@param radius number
---@param height number
---@param round number
---@return SDF3
function sdf.cylinder(radius, height, round) end

---@param majorRadius number
---@param minorRadius number
---@return SDF3
function sdf.torus(majorRadius, minorRadius) end

---@param face2face number
---@param height number
---@return SDF3
function sdf.hexprism(face2face, height) end

---@param triangleHeight number
---@param extrude number
---@return SDF3
function sdf.triprism(triangleHeight, extrude) end

---@param x number
---@param y number
---@param z number
---@param thickness number
---@return SDF3
function sdf.boxframe(x, y, z, thickness) end

---Publish a named solid into the cadpad world so it renders.
---@param name string
---@param obj SDF3
function sdf.register(name, obj) end
