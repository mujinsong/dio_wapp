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

function loadPage(name) {
  const calls = []
  const api = (...args) => {
    const pending = deferred()
    calls.push({ args, ...pending })
    return pending.promise
  }
  let page
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../miniprogram/pages', name, `${name}.js`), 'utf8'), {
    require: () => ({ listPointGrantRequests: api, listMine: api, listReview: api }),
    Page: value => { page = value },
    wx: { showToast() {} }
  })
  page.data = JSON.parse(JSON.stringify(page.data))
  page.setData = update => Object.assign(page.data, update)
  page.data.reviewGroups = [{ group_id: 1 }, { group_id: 2 }]
  page.data.loading = false
  return { page, calls }
}

function result(ids, more = false) {
  return { items: ids.map(id => ({ id, request_status: 'pending' })), next_cursor: more ? ids[ids.length - 1] : 0, has_more: more }
}

for (const name of ['point-grant-approvals', 'point-corrections']) {
  const grants = name === 'point-grant-approvals'
  const key = grants ? 'requests' : 'reviewRequests'
  const load = page => grants ? page.loadRequests() : page.loadCurrent()

  test(`${name}: stale team responses cannot overwrite current results or loading state`, async () => {
    const { page, calls } = loadPage(name)
    if (!grants) page.data.activeTab = 'review'
    const first = load(page)
    page.data.groupIndex = 1
    const second = load(page)
    calls[0].resolve(result([90]))
    await first
    assert.equal(page.data[key].length, 0)
    assert.equal(page.data.loading, true)
    calls[1].resolve(result([80]))
    await second
    assert.equal(page.data[key][0].id, 80)
    assert.equal(page.data.loading, false)
  })

  test(`${name}: duplicate loads are blocked and a failed next page can be retried`, async () => {
    const { page, calls } = loadPage(name)
    if (!grants) page.data.activeTab = 'review'
    const first = load(page)
    calls[0].resolve(result([100, 99], true))
    await first
    const next = page.onReachBottom()
    await page.onReachBottom()
    assert.equal(calls.length, 2)
    assert.equal(calls[1].args[2], 99)
    calls[1].reject(new Error('offline'))
    await next
    assert.equal(page.data[key].length, 2)
    assert.equal(page.data.nextCursor, 99)
    assert.ok(page.data.error)
    const retry = page.retryLoad()
    assert.equal(calls[2].args[2], 99)
    calls[2].resolve(result([98]))
    await retry
    assert.equal(page.data[key].map(item => item.id).join(','), '100,99,98')
    assert.equal(page.data.error, '')
    await page.onReachBottom()
    assert.equal(calls.length, 3)
  })

  test(`${name}: refreshing invalidates an in-flight next page`, async () => {
    const { page, calls } = loadPage(name)
    if (!grants) page.data.activeTab = 'review'
    const first = load(page)
    calls[0].resolve(result([10], true))
    await first
    const next = page.onReachBottom()
    const refresh = load(page)
    calls[2].resolve(result([11]))
    await refresh
    calls[1].resolve(result([9]))
    await next
    assert.equal(page.data[key].map(item => item.id).join(','), '11')
  })
}

test('corrections: changing tabs invalidates old errors', async () => {
  const { page, calls } = loadPage('point-corrections')
  const mine = page.loadCurrent()
  page.data.activeTab = 'review'
  const review = page.loadCurrent()
  calls[1].resolve(result([12]))
  await review
  calls[0].reject(new Error('stale error'))
  await mine
  assert.equal(page.data.error, '')
  assert.equal(page.data.reviewRequests[0].id, 12)
})
