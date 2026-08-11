export async function fillAndSubmit(symbol, addPair = false) {
  const selector = 'input[placeholder="币种或合约地址"], input[placeholder="Symbol or CA"]';

  try {
    const input = document.querySelector(selector);
    if (!input) {
      return {
        ok: false,
        code: 'INPUT_NOT_FOUND',
        message: '未找到币种或合约地址输入框'
      };
    }

    const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
    if (valueSetter) {
      valueSetter.call(input, symbol);
    } else {
      input.value = symbol;
    }

    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new KeyboardEvent('keydown', {
      key: 'Enter',
      code: 'Enter',
      keyCode: 13,
      which: 13,
      bubbles: true,
      cancelable: true
    }));

    if (addPair) {
      await new Promise((resolve) => setTimeout(resolve, 1000));
      const addPairButton = document.evaluate(
        '//button[normalize-space(text())="Add Pair" or normalize-space(text())="添加交易对"]',
        document,
        null,
        XPathResult.FIRST_ORDERED_NODE_TYPE,
        null
      ).singleNodeValue;
      if (!addPairButton) {
        return {
          ok: false,
          code: 'ADD_PAIR_BUTTON_NOT_FOUND',
          message: '未找到 Add Pair 或添加交易对按钮'
        };
      }
      addPairButton.click();
    }

    return { ok: true };
  } catch (error) {
    return {
      ok: false,
      code: 'EXECUTION_ERROR',
      message: error instanceof Error ? error.message : '页面脚本执行失败'
    };
  }
}
