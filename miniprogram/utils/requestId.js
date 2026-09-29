function createRequestId(prefix) {
  const timestamp = Date.now().toString(36)
  const random = Math.random().toString(36).slice(2, 14)
  return `${prefix}_${timestamp}_${random}`
}

module.exports = {
  createRequestId
}
