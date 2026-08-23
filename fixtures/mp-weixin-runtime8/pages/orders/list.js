// Runtime8 Fixture: orders list page logic (Scenario C)
Page({
  data: {
    loading: true,
    orders: []
  },

  onLoad() {
    // Simulate async data load (deterministic delay)
    setTimeout(() => {
      const orders = []
      for (let i = 1; i <= 40; i++) {
        const id = 'LC-' + String(i).padStart(4, '0')
        orders.push({
          id: id,
          status: i % 3 === 0 ? '已完成' : (i % 3 === 1 ? '待发货' : '已发货')
        })
      }
      this.setData({ loading: false, orders })
      console.log('orders:loaded:40 rows')
    }, 500)
  },

  onOrderTap(e) {
    // Row tap — no navigation
  },

  onDetailTap(e) {
    const index = e.currentTarget.dataset.index
    const order = this.data.orders[index]
    if (order && order.id === 'LC-0027') {
      console.warn('orders:detail:warning:LC-0027 opened')
      // Deterministic handled error
      try {
        throw new Error('orders:detail:handled_error:LC-0027')
      } catch (err) {
        console.error('orders:detail:handled:' + err.message)
      }
      console.log('orders:detail:run_id:' + Date.now())
    } else {
      console.log('orders:detail:opened:' + (order ? order.id : 'unknown'))
    }
  },

  triggerError() {
    // Unhandled exception for diagnose testing
    throw new Error('orders:unhandled:trigger_error')
  }
})
