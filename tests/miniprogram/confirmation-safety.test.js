const test = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')

function fixture() {
  const values = new Map([['token', 'token-1'], ['user', { id: 1, points_balance: 100 }]])
  const app = { globalData: { user: values.get('user') } }
  const wx = {
    getStorageSync: key => values.get(key),
    setStorageSync: (key, value) => values.set(key, value),
    removeStorageSync: key => values.delete(key),
    showToast() {}, setNavigationBarTitle() {}
  }
  function load(name, dependencies = {}) {
    const module = { exports: {} }
    vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../miniprogram/utils', `${name}.js`), 'utf8'), { module, wx, require: name => dependencies[name] })
    return module.exports
  }
  const session = load('session')
  function page(name, dependencies) {
    let result
    vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../miniprogram/pages', name, `${name}.js`), 'utf8'), {
      Page: value => { result = value }, wx, getApp: () => app,
      require: name => name.endsWith('/session') ? session : dependencies[name], setTimeout() {}, clearTimeout() {}
    })
    result.data = JSON.parse(JSON.stringify(result.data))
    result.setData = update => Object.assign(result.data, update)
    return result
  }
  return { wx, values, session, load, page, app }
}

for (const outcome of ['success', 'unauthorized', 'network']) {
  test(`request rejects stale ${outcome} after an account switch`, async () => {
    const f = fixture()
    let callback
    f.wx.request = options => { callback = options }
    const request = f.load('request', { './config': { baseURL: 'https://local.test' }, './session': f.session })
    const pending = request.post('/exchange/orders', { goods_id: 1 })
    f.session.invalidate()
    f.values.set('token', 'token-2')
    if (outcome === 'network') callback.fail({ errMsg: 'request:fail timeout' })
    else callback.success({ statusCode: outcome === 'success' ? 200 : 401, data: { error: { code: 'invalid_token' } } })
    await assert.rejects(pending, { code: 'session_changed' })
  })
}

test('request invalidation works even when a later login returns the same token', async () => {
  const f = fixture()
  let callback
  f.wx.request = options => { callback = options }
  const request = f.load('request', { './config': {}, './session': f.session })
  const pending = request.get('/auth/me')
  f.session.invalidate()
  callback.success({ statusCode: 200, data: { user: { id: 1 } } })
  await assert.rejects(pending, { code: 'session_changed' })
})

function adminFixture() {
  const f = fixture(), posts = [], queries = []
  const points = {
    adminAdjustPoints: payload => { posts.push(payload); return new Promise(() => {}) },
    getAdminPointUser: (agency, id) => new Promise(resolve => queries.push({ id, resolve })),
    listAdminUserLedgers: () => Promise.resolve({ items: [] })
  }
  const page = f.page('admin-points', {
    '../../utils/points': points,
    '../../utils/operationRequest': { resolve: () => ({ id: 'fixed-request' }), clear() {} }
  })
  let modal
  f.wx.showModal = options => { modal = options }
  Object.assign(page.data, { loading: false, agencyId: 1, target: { id: 11 }, hasTarget: true, amount: '60', remark: 'checked reason' })
  return { ...f, page, posts, queries, modal: () => modal }
}

test('administrator cannot confirm while a target lookup is pending', async () => {
  const f = adminFixture()
  const search = f.page.loadTarget(22)
  f.page.submitAdjustment()
  assert.equal(f.modal(), undefined)
  f.queries[0].resolve({ id: 22 })
  await search
})

test('administrator confirmation pins amount and reason and blocks a changed target', () => {
  const f = adminFixture()
  f.page.submitAdjustment()
  assert.match(f.modal().content, /#11.*60/)
  f.page.setData({ target: { id: 22 } })
  f.modal().success({ confirm: true })
  assert.equal(f.posts.length, 0)
  f.page.setData({ target: { id: 11 } })
  f.page.submitAdjustment()
  f.page.setData({ amount: '999', remark: 'changed reason', mode: 'deduct' })
  f.modal().success({ confirm: true })
  assert.equal(f.posts[0].user_id, 11)
  assert.equal(f.posts[0].delta_points, 60)
  assert.equal(f.posts[0].remark, 'checked reason')
  f.modal().success({ confirm: true })
  assert.equal(f.posts.length, 1)
})

test('administrator ignores stale target queries and old-account confirmations', async () => {
  const f = adminFixture()
  const first = f.page.loadTarget(22), second = f.page.loadTarget(33)
  f.queries[1].resolve({ id: 33 })
  await second
  f.queries[0].resolve({ id: 22 })
  await first
  assert.equal(f.page.data.target.id, 33)
  f.page.submitAdjustment()
  f.session.invalidate()
  f.modal().success({ confirm: true })
  assert.equal(f.posts.length, 0)
})

for (const name of ['shop', 'goods-detail']) {
  function shopFixture() {
    const f = fixture(), calls = []
    const page = f.page(name, {
      '../../utils/shop': { exchangeGoods: (...args) => new Promise((resolve, reject) => calls.push({ args, resolve, reject })) },
      '../../utils/operationRequest': { resolve: () => ({ id: 'same-exchange-id' }), clear() {} }
    })
    const goods = { id: 3, price_points: 30, stock: 5, can_buy: true, name: 'Item' }
    Object.assign(page.data, { user: { id: 1, points_balance: 100 }, goods: name === 'shop' ? [goods] : goods, allGoods: [goods] })
    return { ...f, page, goods, calls }
  }

  test(`${name}: sends the price displayed in the confirmation and ignores duplicate submits`, async () => {
    const f = shopFixture()
    let modal
    f.wx.showModal = value => { modal = value }
    f.page.exchange({ currentTarget: { dataset: { goodsId: 3 } } })
    f.goods.price_points = 60
    modal.success({ confirm: true })
    assert.equal(f.calls[0].args[2], 30)
    await f.page.submitExchange(f.goods)
    assert.equal(f.calls.length, 1)
    f.calls[0].resolve({ after_points: 70, remaining_stock: 4 })
    await new Promise(resolve => setImmediate(resolve))
  })

  test(`${name}: late exchange cannot overwrite a different account or clear its operation`, async () => {
    const f = shopFixture()
    const pending = f.page.submitExchange(f.goods)
    f.session.invalidate()
    const newUser = { id: 2, points_balance: 500 }
    f.values.set('token', 'token-2')
    f.values.set('user', newUser)
    f.app.globalData.user = newUser
    f.calls[0].resolve({ after_points: 70, remaining_stock: 4 })
    await pending
    assert.equal(f.values.get('user').id, 2)
    assert.equal(f.app.globalData.user.points_balance, 500)
    assert.notEqual(f.page.data.balance, 70)
    assert.ok(f.page.data.pendingExchangeRequest)
  })

  test(`${name}: price mismatch refreshes the product without automatically resubmitting`, async () => {
    const f = shopFixture()
    let refreshed = 0
    f.page[name === 'shop' ? 'loadPage' : 'loadDetail'] = () => { refreshed++; return Promise.resolve() }
    const pending = f.page.submitExchange(f.goods)
    f.calls[0].reject(Object.assign(new Error('changed'), { code: 'goods_price_changed' }))
    await pending
    assert.equal(refreshed, 1)
    assert.equal(f.calls.length, 1)
    assert.match(f.page.data.error, /重新确认/)
  })
}

test('shop API includes the confirmed price in the request body', async () => {
  const f = fixture()
  let sent
  const shop = f.load('shop', { './request': { post: (url, data) => { sent = data; return Promise.resolve({ result: {} }) } } })
  await shop.exchangeGoods(3, 'same-exchange-id', 30)
  assert.equal(sent.expected_price_points, 30)
  assert.equal(sent.request_id, 'same-exchange-id')
})
