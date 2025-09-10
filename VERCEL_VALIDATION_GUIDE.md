# 如何验证 Vercel 超时配置是否生效

按照以下步骤来确认你的 `vercel.json` 配置是否已成功应用。

## 🚀 Step 1: 部署并检查 Vercel Dashboard

1.  **提交并部署**：
    确保你已经将 `vercel.json` 和新创建的 `app/api/test-timeout/route.ts` 文件提交到你的 Git 仓库并触发了一次新的 Vercel 部署。
    ```bash
    git add .
    git commit -m "feat: Configure Vercel timeout and add test route"
    git push
    ```

2.  **在 Vercel 中检查配置**：
    - 等待部署完成。
    - 进入你的 Vercel 项目的 **Dashboard**。
    - 点击顶部导航栏的 **Functions** 标签。
    - 找到你的 API 路由（例如 `api/user` 或新创建的 `api/test-timeout`）。
    - 在它的配置详情里，**Timeout** 应该显示为 **60s**。如果仍然是 10s，说明 `vercel.json` 配置未生效。

    *(你可以在 Vercel Dashboard 的函数详情页看到超时时间)*

## 🔬 Step 2: 使用测试 API 进行实际验证

1.  **确认部署成功**：
    确保上一步的部署已完成。

2.  **打开浏览器控制台**：
    在你部署的 Vercel 网站上，打开开发者工具（F12），并切换到 **Console** 标签。

3.  **运行测试代码**：
    将以下代码粘贴到控制台中并按回车：
    ```javascript
    console.log("🚀 Starting 50-second timeout test...");
    console.log("🕒 Please wait, this will take about 50 seconds.");

    // 发起一个需要 50 秒才能完成的请求
    fetch('/api/test-timeout', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ delay: 50000 }) // 50,000 毫秒 = 50 秒
    })
    .then(response => {
      console.log("...Response received from server.");
      if (!response.ok) {
        // Vercel 超时会返回 504 Gateway Timeout
        console.error(`❌ Test Failed! Status: ${response.status} ${response.statusText}`);
        throw new Error(`HTTP error! Status: ${response.status}`);
      }
      return response.json();
    })
    .then(data => {
      console.log("✅ Success! Vercel timeout is working correctly.", data);
    })
    .catch(error => {
      console.error("❌ Error! The timeout test failed.", error);
      console.log("💡 Tip: Check the Vercel function logs for more details.");
    });
    ```

## 📊 预期结果

-   **如果配置成功** ✅：
    大约 50 秒后，你会在控制台看到 `✅ Success!` 的消息。这证明你的 Vercel 函数可以运行超过 10 秒。

-   **如果配置失败** ❌：
    大约 10-15 秒后，请求会失败，你会在控制台看到一个 **504 Gateway Timeout** 或类似的网络错误。这说明配置没有生效。

## 🩺 故障排除

如果测试失败，请检查：
1.  **文件名和路径**：确保文件名是 `vercel.json` 并且位于项目的根目录。
2.  **JSON 格式**：确认 `vercel.json` 的内容是有效的 JSON。
3.  **Vercel 日志**：在 Vercel Dashboard 的 **Logs** 或 **Functions** 标签页查看部署和运行时日志，可能会有关于配置错误的提示。
4.  **Next.js 版本**：确保你的 Next.js 版本支持 `maxDuration`。对于 App Router，这是推荐的方式。对于 Pages Router，配置在 `vercel.json` 中。我提供的代码结合了两者，兼容性最好。
