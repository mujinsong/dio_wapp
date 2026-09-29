let version = 0

function capture() {
  return { version, token: wx.getStorageSync('token') }
}

function isCurrent(snapshot) {
  return snapshot.version === version && snapshot.token === wx.getStorageSync('token')
}

function changedError() {
  const error = new Error('登录状态已变化，请刷新后重试')
  error.code = 'session_changed'
  return error
}

module.exports = {
  capture,
  isCurrent,
  changedError,
  assertCurrent(snapshot) {
    if (!isCurrent(snapshot)) throw changedError()
  },
  invalidate() {
    version++
  }
}
