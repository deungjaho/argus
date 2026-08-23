// Runtime8 fixture app — isolated Mini Program for Argus acceptance testing.
// Reproduces interaction shapes from real Lacoeur workflows without
// depending on or modifying production business code.

App({
  globalData: {
    runId: 0,
    keyboardHideCount: 0
  },
  onLaunch() {
    this.globalData.runId = Date.now()
  },
  nextRunId() {
    this.globalData.runId = Date.now()
    return this.globalData.runId
  }
})
