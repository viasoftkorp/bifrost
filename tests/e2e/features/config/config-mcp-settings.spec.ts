import { coreConfigApi } from '../../core/actions/api'
import { expect, test } from '../../core/fixtures/base.fixture'

test.describe('MCP Settings', () => {
  // Run all config tests serially to avoid parallel writes to the same config/store
  test.describe.configure({ mode: 'serial' })

  test('should display MCP settings form', async ({ configSettingsPage }) => {
    await configSettingsPage.goto('mcp-gateway')

    await expect(configSettingsPage.page.getByTestId('mcp-settings-view')).toBeVisible()
    await expect(configSettingsPage.page.getByTestId('mcp-agent-depth-input')).toBeVisible()
    await expect(configSettingsPage.page.getByTestId('mcp-tool-timeout-input')).toBeVisible()
    // Binding level sits inside the collapsed Code Mode Settings section.
    await configSettingsPage.page.getByTestId('mcp-settings-code-mode-trigger').click()
    await expect(configSettingsPage.page.getByTestId('mcp-binding-level')).toBeVisible()
  })

  test('should have save button disabled when no changes', async ({ configSettingsPage }) => {
    await configSettingsPage.goto('mcp-gateway')

    const saveBtn = configSettingsPage.page.getByTestId('mcp-settings-save-btn')
    await expect(saveBtn).toBeVisible()
    await expect(saveBtn).toBeDisabled()
  })

  test('should save the OpenAPI spec size limit', async ({ configSettingsPage, request }) => {
    const original = (await coreConfigApi.get(request)).client_config.mcp_openapi_max_spec_mb ?? 0
    try {
      await configSettingsPage.goto('mcp-gateway')
      const input = configSettingsPage.page.getByTestId('mcp-openapi-max-spec-mb-input')
      await expect(input).toHaveValue(String(original))

      // Differs from the stored value, so the form is dirty and Save enables.
      const target = original === 64 ? 65 : 64
      await input.fill(String(target))
      const saveBtn = configSettingsPage.page.getByTestId('mcp-settings-save-btn')
      await expect(saveBtn).toBeEnabled()
      await saveBtn.click()
      await expect.poll(async () => (await coreConfigApi.get(request)).client_config.mcp_openapi_max_spec_mb).toBe(target)

      await configSettingsPage.page.reload()
      await expect(input).toHaveValue(String(target))
    } finally {
      await coreConfigApi.updateClientConfig(request, { mcp_openapi_max_spec_mb: original })
    }
  })

  test('should save the Bifrost MCP spec URL and writes switch', async ({ configSettingsPage, request }) => {
    const before = (await coreConfigApi.get(request)).client_config
    const originalSpecURL = String(before.bifrost_mcp_spec_url ?? '')
    const originalAllowWrites = before.bifrost_mcp_allow_writes ?? null
    try {
      await configSettingsPage.goto('mcp-gateway')
      await expect(configSettingsPage.page.getByTestId('bifrost-mcp-settings')).toBeVisible()
      const specInput = configSettingsPage.page.getByTestId('bifrost-mcp-spec-url-input')
      const writesSwitch = configSettingsPage.page.getByTestId('bifrost-mcp-allow-writes-switch')
      await expect(specInput).toHaveValue(originalSpecURL)

      await specInput.fill('openapi.json')
      if ((await writesSwitch.getAttribute('aria-checked')) === 'true') {
        await writesSwitch.click()
      }
      const saveBtn = configSettingsPage.page.getByTestId('mcp-settings-save-btn')
      await expect(saveBtn).toBeEnabled()
      await saveBtn.click()
      await expect
        .poll(async () => {
          const cc = (await coreConfigApi.get(request)).client_config
          return [cc.bifrost_mcp_spec_url, cc.bifrost_mcp_allow_writes]
        })
        .toEqual(['openapi.json', false])

      await configSettingsPage.page.reload()
      await expect(specInput).toHaveValue('openapi.json')
      await expect(writesSwitch).toHaveAttribute('aria-checked', 'false')
    } finally {
      await coreConfigApi.updateClientConfig(request, { bifrost_mcp_spec_url: originalSpecURL, bifrost_mcp_allow_writes: originalAllowWrites })
    }
  })
})
