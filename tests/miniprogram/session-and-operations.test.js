const test = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')

function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function load(name, env) {
  const module = { exports: {} }
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../miniprogram/utils', `${name}.js`), 'utf8'), { module, ...env })
  return module.exports
}

function storage() {
  const values = new Map([['user', { id: 1 }], ['token', 'token-1']])
  return {
    getStorageSync: key => values.get(key),
    setStorageSync: (key, value) => values.set(key, value),
    removeStorageSync: key => values.delete(key)
  }
}

test('unconfirmed operations retain IDs after 16 minutes, app reload and 10 days', () => {
  const wx = storage()
  let now = 1000, next = 0
  const env = { wx, Date: { now: () => now }, require: () => ({ createRequestId: () => `request-${++next}` }) }
  const ops = load('operationRequest', env)
  const payload = { goods_id: 1 }
  const first = ops.resolve('exchange', null, payload, true)
  now += 16 * 60 * 1000
  assert.equal(ops.resolve('exchange', first, payload, true).id, first.id)
  now += 10 * 24 * 60 * 60 * 1000
  assert.equal(load('operationRequest', env).resolve('exchange', null, payload, true).id, first.id)
  ops.clear('exchange', first.id, true)
  assert.notEqual(ops.resolve('exchange', null, payload, true).id, first.id)
})

test('pending operations survive other payloads and are isolated by account', () => {
  const wx = storage()
  let next = 0
  const ops = load('operationRequest', { wx, require: () => ({ createRequestId: () => `request-${++next}` }) })
  const first = ops.resolve('exchange', null, { goods_id: 1 }, true)
  const other = ops.resolve('exchange', first, { goods_id: 2 }, true)
  assert.equal(ops.resolve('exchange', other, { goods_id: 1 }, true).id, first.id)
  wx.setStorageSync('user', { id: 2 })
  const secondUser = ops.resolve('exchange', first, { goods_id: 1 }, true)
  assert.notEqual(secondUser.id, first.id)
  ops.clear('exchange', first.id, true)
  assert.equal(ops.resolve('exchange', null, { goods_id: 1 }, true).id, secondUser.id)
  wx.setStorageSync('user', { id: 1 })
  assert.equal(ops.resolve('exchange', null, { goods_id: 1 }, true).id, first.id)
  ops.clear('exchange', other.id, true)
  assert.equal(ops.resolve('exchange', null, { goods_id: 1 }, true).id, first.id)
})

test('pending operation from previous client version keeps its original ID', () => {
  const wx = storage()
  wx.setStorageSync('pending_operation_exchange', { id: 'old-request', fingerprint: JSON.stringify({ goods_id: 1 }), created_at: 1 })
  const ops = load('operationRequest', { wx, require: () => ({ createRequestId: () => 'new-request' }) })
  assert.equal(ops.resolve('exchange', null, { goods_id: 1 }, true).id, 'old-request')
  assert.equal(wx.getStorageSync('pending_operation_exchange'), undefined)
})

function authFixture() {
  const wx = storage(), calls = [], app = { globalData: { user: { id: 1 } } }
  const request = () => { const call = deferred(); calls.push(call); return call.promise }
  const session = load('session', { wx })
  const auth = load('auth', { wx, getApp: () => app, require: name => name === './session' ? session : name === './config' ? { useMockLogin: true, mockOpenid: 'test' } : { get: request, post: request } })
  return { auth, wx, calls, app }
}

test('late profile response cannot restore a logged-out user', async () => {
  const { auth, wx, calls, app } = authFixture()
  const profile = auth.me()
  auth.logout()
  calls[0].resolve({ user: { id: 1 } })
  await assert.rejects(profile, { code: 'session_changed' })
  assert.equal(wx.getStorageSync('user'), undefined)
  assert.equal(wx.getStorageSync('token'), undefined)
  assert.equal(app.globalData.user, null)
})

test('old profile authorization error cannot invalidate a new login', async () => {
  const { auth, wx, calls } = authFixture()
  const profile = auth.me()
  auth.logout()
  const login = auth.login()
  await Promise.resolve()
  calls[1].resolve({ token: 'token-2', user: { id: 2 } })
  await login
  calls[0].reject(Object.assign(new Error('expired'), { code: 'invalid_token' }))
  await assert.rejects(profile, { code: 'session_changed' })
  assert.equal(wx.getStorageSync('user').id, 2)
  assert.equal(wx.getStorageSync('token'), 'token-2')
})

test('concurrent login uses one request and logout invalidates its result', async () => {
  const { auth, wx, calls } = authFixture()
  const login = auth.login()
  assert.equal(auth.login(), login)
  await Promise.resolve()
  assert.equal(calls.length, 1)
  auth.logout()
  calls[0].resolve({ token: 'late-token', user: { id: 1 } })
  await assert.rejects(login, { code: 'session_changed' })
  assert.equal(wx.getStorageSync('token'), undefined)
})

test('older profile response cannot overwrite a newer balance', async () => {
  const { auth, wx, calls } = authFixture()
  const first = auth.me(), second = auth.me()
  calls[1].resolve({ user: { id: 1, points_balance: 120 } })
  await second
  calls[0].resolve({ user: { id: 1, points_balance: 60 } })
  await assert.rejects(first, { code: 'session_changed' })
  assert.equal(wx.getStorageSync('user').points_balance, 120)
})

test('home ignores a profile result arriving after logout and a new login', async () => {
  const oldProfile = deferred(), newLogin = deferred()
  let page
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../miniprogram/pages/index/index.js'), 'utf8'), {
    require: () => ({ me: () => oldProfile.promise, login: () => newLogin.promise, logout() {} }),
    Page: value => { page = value }, wx: { showToast: () => assert.fail('stale error shown') }
  })
  page.data = JSON.parse(JSON.stringify(page.data))
  page.setData = update => Object.assign(page.data, update)
  page.handleRefresh()
  page.handleLogout()
  page.handleLogin()
  oldProfile.resolve({ id: 1, nickname: 'Old account' })
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(page.data.user, null)
  assert.equal(page.data.loading, true)
  newLogin.resolve({ id: 2, nickname: 'New account' })
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(page.data.user.id, 2)
  assert.equal(page.data.loading, false)
})
