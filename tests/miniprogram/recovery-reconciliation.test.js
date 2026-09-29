const test = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')

function fixture() {
  const storage = new Map([['token', 'token-1'], ['user', { id: 1 }]])
  const modals = [], navigation = [], cleared = []
  const wx = {
    getStorageSync: key => storage.get(key),
    setStorageSync: (key, value) => storage.set(key, value),
    removeStorageSync: key => storage.delete(key),
    showModal: options => modals.push(options),
    navigateTo: options => navigation.push(options.url),
    showToast() {}
  }
  function load(name, dependencies = {}) {
    const module = { exports: {} }
    vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../miniprogram/utils', `${name}.js`), 'utf8'), { module, wx, require: key => dependencies[key] })
    return module.exports
  }
  const session = load('session')
  const operation = { id: 'request-1', fingerprint: '{"goods_id":7}', actor_id: '1' }
  const page = { data: { pendingExchangeRequest: operation }, setData(update) { Object.assign(this.data, update) } }
  const response = { completed: true, request_id: operation.id, goods_id: 7, order: { id: 9, order_no: 'NO9', goods_name: 'goods', points_cost: 10, status: 'pending' } }
  let resolve, reject
  const recovery = load('exchangeRecovery', {
    './session': session,
    './shop': { recoverExchange: () => new Promise((yes, no) => { resolve = yes; reject = no }) },
    './operationRequest': { clear: (...args) => cleared.push(args) }
  })
  return { wx, storage, modals, navigation, cleared, load, session, operation, page, response, recovery, resolve: value => resolve(value), reject: error => reject(error) }
}

const tick = () => new Promise(resolve => setImmediate(resolve))

test('exchange recovery opens the original order without clearing the request', async () => {
  const f = fixture()
  const pending = f.recovery.recover(f.page, 7, f.operation, f.session.capture())
  f.resolve(f.response)
  await tick()
  f.modals[0].success({ confirm: true })
  await pending
  assert.match(f.navigation[0], /id=9$/)
  assert.equal(f.cleared.length, 0)
})

test('exchange recovery only resets after two explicit acknowledgements', async () => {
  const f = fixture()
  const pending = f.recovery.recover(f.page, 7, f.operation, f.session.capture())
  f.resolve(f.response)
  await tick()
  f.modals[0].success({ cancel: true })
  await tick()
  assert.equal(f.cleared.length, 0)
  f.modals[1].success({ confirm: true })
  await pending
  assert.equal(f.cleared[0][1], f.operation.id)
  assert.equal(f.page.data.pendingExchangeRequest, null)
  assert.equal(f.navigation.length, 0)
})

for (const change of ['session', 'unload', 'request']) {
  test(`exchange recovery cannot reset after ${change} changes`, async () => {
    const f = fixture()
    const pending = f.recovery.recover(f.page, 7, f.operation, f.session.capture())
    f.resolve(f.response)
    await tick()
    f.modals[0].success({ cancel: true })
    await tick()
    if (change === 'session') f.session.invalidate()
    if (change === 'unload') f.page._unloaded = true
    if (change === 'request') f.page.data.pendingExchangeRequest = { id: 'new-request' }
    f.modals[1].success({ confirm: true })
    await pending
    assert.equal(f.cleared.length, 0)
  })
}

test('legacy archived request links to history without guessing an order', async () => {
  const f = fixture()
  const pending = f.recovery.recover(f.page, 7, f.operation, f.session.capture())
  f.resolve(Object.assign({}, f.response, { order: null }))
  await tick()
  f.modals[0].success({ confirm: true })
  await pending
  assert.equal(f.navigation[0], '/pages/exchange-orders/exchange-orders')
  assert.equal(f.cleared.length, 0)
})

test('failed recovery never releases a possibly unfinished exchange', async () => {
  const f = fixture()
  const pending = f.recovery.recover(f.page, 7, f.operation, f.session.capture())
  f.reject(new Error('not found'))
  await pending
  assert.equal(f.cleared.length, 0)
  assert.equal(f.modals.length, 0)
  assert.equal(f.page.data.pendingExchangeRequest.id, f.operation.id)
})

test('recovery rejects a response belonging to another product', async () => {
  const f = fixture()
  const pending = f.recovery.recover(f.page, 7, f.operation, f.session.capture())
  f.resolve(Object.assign({}, f.response, { goods_id: 8 }))
  await pending
  assert.equal(f.cleared.length, 0)
  assert.equal(f.modals.length, 0)
})

test('pending exchange listing only includes the current actor', () => {
  const f = fixture()
  f.storage.set('pending_operations_exchange_1', [f.operation, { id: 'other', actor_id: '2' }])
  const operations = f.load('operationRequest', { './requestId': {} })
  assert.equal(operations.list('exchange').length, 1)
  f.storage.set('user', { id: 2 })
  assert.equal(operations.list('exchange').length, 0)
})

function reportFixture() {
  const f = fixture()
  let page
  const calls = []
  const profiles = [], reports = []
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../miniprogram/pages/admin-report/admin-report.js'), 'utf8'), {
    Page: value => { page = value }, wx: f.wx,
    require: name => name.endsWith('/session') ? f.session : name.endsWith('/adminReport') ? {
      reconcile: (agency, kind, cursor) => new Promise((resolve, reject) => calls.push({ agency, kind, cursor, resolve, reject })),
      overview: agency => new Promise((resolve, reject) => reports.push({ agency, resolve, reject }))
    } : { me: () => new Promise((resolve, reject) => profiles.push({ resolve, reject })) }
  })
  page.data = JSON.parse(JSON.stringify(page.data))
  page.data.agencyId = 1
  page.setData = update => Object.assign(page.data, update)
  return { ...f, page, calls, profiles, reports }
}

test('reconciliation resumes from the last successful batch after an error', async () => {
  const f = reportFixture()
  const pending = f.page.reconcile()
  await f.page.reconcile()
  assert.equal(f.calls.length, 1)
  f.calls[0].resolve({ checked: 20, has_more: true, next_cursor: 20, issues: [] })
  await tick()
  f.calls[1].reject(new Error('network failed'))
  await pending
  assert.equal(f.page.data.reconciliation.checked, 20)
  const resume = f.page.reconcile()
  assert.equal(f.calls[2].cursor, 20)
  f.calls[2].reject(new Error('still failed'))
  await resume
  assert.equal(f.page.data.reconciliation.checked, 20)
})

test('reconciliation covers all three categories with bounded visible issues', async () => {
  const f = reportFixture()
  const pending = f.page.reconcile()
  for (let i = 0; i < 3; i++) {
    await tick()
    f.calls[i].resolve({ checked: 20, has_more: false, issues: Array.from({ length: 80 }, (_, n) => ({ kind: f.calls[i].kind, id: n, severity: 'error' })) })
  }
  await pending
  assert.deepEqual(f.calls.map(call => call.kind), ['accounts', 'orders', 'stock'])
  assert.equal(f.page.data.reconciliation.done, true)
  assert.equal(f.page.data.reconciliation.issueCount, 240)
  assert.equal(f.page.data.reconciliation.issues.length, 200)
})

for (const change of ['session', 'unload', 'agency']) {
  test(`reconciliation ignores a response after ${change} changes`, async () => {
    const f = reportFixture()
    const pending = f.page.reconcile()
    if (change === 'session') f.session.invalidate()
    if (change === 'unload') f.page.onUnload()
    if (change === 'agency') f.page.data.agencyId = 2
    f.calls[0].resolve({ checked: 20, has_more: false, issues: [] })
    await pending
    assert.equal(f.page.data.reconciliation.checked, 0)
    assert.equal(f.calls.length, 1)
  })
}

test('report: loss of administrator membership clears all previously visible data', async () => {
  const f = reportFixture()
  f.page.setData({ report: { secret: true }, metrics: { count: 99 }, reconciliation: { checked: 10 }, staffStats: [1], movements: [1] })
  const pending = f.page.loadAdmin()
  assert.equal(f.page.data.report, null)
  assert.equal(f.page.data.reconciliation, null)
  f.profiles[0].resolve({ admin_agencies: [] })
  await pending
  assert.equal(f.page.data.agencyId, 0)
  assert.equal(f.page.data.staffStats.length, 0)
  assert.equal(f.page.data.movements.length, 0)
  assert.equal(f.page.data.loading, false)
  assert.equal(f.reports.length, 0)
})

test('report: an older permission lookup cannot replace a newer agency or stop its loading', async () => {
  const f = reportFixture()
  const old = f.page.loadAdmin(), fresh = f.page.loadAdmin()
  f.profiles[1].resolve({ admin_agencies: [{ agency_id: 2, agency_name: 'new' }] })
  await tick()
  f.profiles[0].resolve({ admin_agencies: [{ agency_id: 1, agency_name: 'old' }] })
  await old
  assert.equal(f.page.data.agencyId, 2)
  assert.equal(f.page.data.loading, true)
  assert.equal(f.reports.length, 1)
  f.reports[0].resolve({ metrics: { count: 2 } })
  await fresh
  assert.equal(f.page.data.metrics.count, 2)
})

test('report: revoked reconciliation permission invalidates an in-flight overview', async () => {
  const f = reportFixture()
  const overview = f.page.loadReport(), audit = f.page.reconcile()
  f.calls[0].reject(Object.assign(new Error('forbidden'), { code: 'admin_forbidden' }))
  await audit
  assert.equal(f.page.data.agencyId, 0)
  assert.equal(f.page.data.reconciliation, null)
  f.reports[0].resolve({ metrics: { secret: 100 } })
  await overview
  assert.equal(f.page.data.report, null)
})

test('report: returning to the page rechecks permission and clears the old report', async () => {
  const f = reportFixture()
  f.page.onShow()
  f.page.data.report = { secret: true }
  f.session.invalidate()
  const pending = f.page.onShow()
  assert.equal(f.page.data.report, null)
  f.profiles[0].reject(new Error('offline'))
  await pending
  assert.equal(f.page.data.agencyId, 0)
})

test('operations: malformed neighboring entries never discard a valid pending request', () => {
  const f = fixture()
  const key = 'pending_operations_exchange_1'
  f.storage.set(key, [null, f.operation, false])
  const ops = f.load('operationRequest', { './requestId': { createRequestId: () => 'new-request' } })
  assert.equal(ops.resolve('exchange', null, { goods_id: 7 }, true).id, f.operation.id)
  ops.clear('exchange', 'some-other-request', true)
  assert.equal(ops.resolve('exchange', null, { goods_id: 7 }, true).id, f.operation.id)
  ops.clear('exchange', f.operation.id, true)
  assert.equal(ops.list('exchange').length, 0)
})

test('orders: storage failure during recovery releases the button without querying the server', async () => {
  const f = fixture()
  let page, queries = 0
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../miniprogram/pages/exchange-orders/exchange-orders.js'), 'utf8'), {
    Page: value => { page = value }, wx: f.wx,
    require: name => name.endsWith('/session') ? f.session : name.endsWith('/exchangeRecovery') ? { recover: () => { queries++ } } : {
      resolve: () => { throw new Error('storage full') }, list: () => [f.operation]
    }
  })
  page.data = JSON.parse(JSON.stringify(page.data))
  page.setData = update => Object.assign(page.data, update)
  page._pageSession = f.session.capture()
  page.data.pendingExchanges = [Object.assign({}, f.operation, { goods_id: 7 })]
  await page.recoverExchange({ currentTarget: { dataset: { requestId: f.operation.id } } })
  assert.equal(page.data.recovering, false)
  assert.equal(queries, 0)
  assert.match(page.data.error, /保存失败/)
})
