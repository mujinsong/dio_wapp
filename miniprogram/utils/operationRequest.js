const requestId = require('./requestId')

function actorID() {
  const user = wx.getStorageSync('user')
  return String(user && user.id || 'anonymous')
}

function storageKey(prefix, actor) {
  return `pending_operations_${prefix}_${actor}`
}

function isReusable(operation, fingerprint, actor) {
  return Boolean(
    operation &&
    operation.id &&
    operation.fingerprint === fingerprint &&
    (operation.actor_id === undefined || operation.actor_id === actor)
  )
}

function resolve(prefix, current, payload, persist) {
  const fingerprint = JSON.stringify(payload || {})
  const actor = actorID()
  const key = storageKey(prefix, actor)
  const saved = persist ? wx.getStorageSync(key) : null
  const operations = Array.isArray(saved) ? saved : []
  const legacyKey = `pending_operation_${prefix}`
  const legacy = persist ? wx.getStorageSync(legacyKey) : null
  // An unresolved operation keeps its ID regardless of elapsed time or intervening operations.
  const reusable = operations.find(item => isReusable(item, fingerprint, actor)) ||
    (isReusable(current, fingerprint, actor) ? current : null) ||
    (isReusable(legacy, fingerprint, actor) ? legacy : null)
  const operation = reusable ? Object.assign({}, reusable, { actor_id: actor }) : {
    id: requestId.createRequestId(prefix),
    fingerprint,
    created_at: Date.now(),
    actor_id: actor
  }
  if (persist) {
    const next = operations.filter(item => !item || item.id !== operation.id)
    next.push(operation)
    wx.setStorageSync(key, next)
    if (legacy && legacy.id === operation.id) wx.removeStorageSync(legacyKey)
  }
  return operation
}

function clear(prefix, operationId, persist) {
  if (!persist) {
    return
  }
  if (!operationId) return
  const key = storageKey(prefix, actorID())
  const stored = wx.getStorageSync(key)
  if (!Array.isArray(stored)) return
  const remaining = stored.filter(item => !item || item.id !== operationId)
  if (remaining.length) wx.setStorageSync(key, remaining)
  else wx.removeStorageSync(key)
}

module.exports = {
  list(prefix) {
    const actor = actorID()
    const stored = wx.getStorageSync(storageKey(prefix, actor))
    const operations = Array.isArray(stored) ? stored.filter(item => item && item.actor_id === actor) : []
    const legacy = wx.getStorageSync(`pending_operation_${prefix}`)
    if (legacy && legacy.id && (legacy.actor_id === undefined || legacy.actor_id === actor) && !operations.some(item => item.id === legacy.id)) operations.push(legacy)
    return operations
  },
  resolve,
  clear
}
