const test = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')

const rows = top => Array.from({ length: 20 }, (_, i) => ({ id: top - i }))
const result = top => ({ items: rows(top), next_cursor: top - 19, has_more: true })

for (const [name, method, api, key] of [
  ['point-ledgers', 'loadPage', 'listMyLedgers', 'items'],
  ['exchange-orders', 'loadOrders', 'listOrders', 'orders']
]) {
  function fixture() {
    let page
    const calls = []
    const request = cursor => new Promise((resolve, reject) => calls.push({ cursor, resolve, reject }))
    const wx = { showToast() {}, getStorageSync: () => 'token-1' }
    const sessionModule = { exports: {} }
    vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../miniprogram/utils/session.js'), 'utf8'), { module: sessionModule, wx })
    const session = sessionModule.exports
    vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../miniprogram/pages', name, `${name}.js`), 'utf8'), {
      Page: value => { page = value }, require: key => key.endsWith('/session') ? session : ({ [api]: request, list: () => [] }), wx
    })
    page.data = JSON.parse(JSON.stringify(page.data))
    page.setData = update => Object.assign(page.data, update)
    page._pageSession = session.capture()
    page.data[key] = rows(100)
    Object.assign(page.data, { cursor: 81, hasMore: true, loading: false })
    return { page, calls, session }
  }

  test(`${name}: refresh invalidates old page so record 81 is not skipped`, async () => {
    const { page, calls } = fixture()
    const next = page[method](false), refresh = page[method](true)
    calls[1].resolve(result(101))
    await refresh
    calls[0].resolve(result(80))
    await next
    assert.equal(page.data.cursor, 82)
    assert.equal(page.data[key].length, 20)
    const correctNext = page[method](false)
    assert.equal(calls[2].cursor, 82)
    calls[2].resolve(result(81))
    await correctNext
    assert.equal(page.data[key].length, 40)
    assert.equal(page.data[key][20].id, 81)
  })

  test(`${name}: duplicate loads and loads during refresh are ignored`, async () => {
    const { page, calls } = fixture()
    const next = page.onReachBottom()
    await page.onReachBottom()
    assert.equal(calls.length, 1)
    const refresh = page[method](true)
    await page.onReachBottom()
    assert.equal(calls.length, 2)
    calls[0].reject(new Error('stale error'))
    await next
    assert.equal(page.data.loading, true)
    assert.equal(page.data.error, '')
    calls[1].resolve(result(101))
    await refresh
    assert.equal(page.data.loading, false)
  })

  test(`${name}: failed next page retains records and cursor for retry`, async () => {
    const { page, calls } = fixture()
    const next = page[method](false)
    calls[0].reject(new Error('offline'))
    await next
    assert.equal(page.data[key].length, 20)
    assert.equal(page.data.cursor, 81)
    const retry = page[method](false)
    assert.equal(calls[1].cursor, 81)
    calls[1].resolve(result(80))
    await retry
    assert.equal(page.data[key].length, 40)
    assert.equal(page.data.error, '')
  })

  test(`${name}: response after leaving page does not update it`, async () => {
    const { page, calls } = fixture()
    const next = page[method](false)
    page.onUnload()
    page.setData = () => assert.fail('updated unloaded page')
    calls[0].resolve(result(80))
    await next
  })

  if (name === 'exchange-orders') {
    test('orders: account switch clears old recovery state and bypasses stale loading', async () => {
      const { page, calls, session } = fixture()
      const old = page.loadOrders(false)
      page.setData({ recovering: true, pendingExchangeRequest: { id: 'old' } })
      session.invalidate()
      const fresh = page.onShow()
      assert.equal(page.data.orders.length, 0)
      assert.equal(page.data.recovering, false)
      assert.equal(page.data.pendingExchangeRequest, null)
      assert.equal(calls.length, 2)
      assert.equal(calls[1].cursor, 0)
      calls[0].resolve(result(80))
      await old
      assert.equal(page.data.loading, true)
      assert.equal(page.data.orders.length, 0)
      calls[1].resolve(result(200))
      await fresh
      assert.equal(page.data.orders[0].id, 200)
    })

    test('orders: session change blocks stale response even before onShow runs', async () => {
      const { page, calls, session } = fixture()
      const pending = page.loadOrders(false)
      session.invalidate()
      calls[0].resolve(result(80))
      await pending
      assert.equal(page.data.orders.length, 20)
      assert.equal(page.data.cursor, 81)
    })
  }
}
