import assert from 'node:assert/strict';
import test from 'node:test';

import { fillAndSubmit } from '../extension/page-action.js';
import { DEFAULT_RELAY_URL, validateRelayUrl } from '../extension/settings.js';

test('default relay URL is accepted', () => {
  const result = validateRelayUrl(DEFAULT_RELAY_URL);
  assert.equal(result.ok, true);
  assert.equal(result.url, DEFAULT_RELAY_URL);
});

test('relay URL rejects non-loopback and unexpected paths', () => {
  assert.equal(validateRelayUrl('ws://192.168.1.10:8787/extension').ok, false);
  assert.equal(validateRelayUrl('wss://127.0.0.1:8787/extension').ok, false);
  assert.equal(validateRelayUrl('ws://127.0.0.1:8787/other').ok, false);
  assert.equal(validateRelayUrl('not-a-url').ok, false);
});

test('page action reports a missing input', async () => {
  const originalDocument = globalThis.document;
  globalThis.document = { querySelector: () => null };
  try {
    const result = await fillAndSubmit('BTC');
    assert.deepEqual(result, {
      ok: false,
      code: 'INPUT_NOT_FOUND',
      message: '未找到币种或合约地址输入框'
    });
  } finally {
    globalThis.document = originalDocument;
  }
});

test('page action sets symbol before dispatching input and Enter', async () => {
  const originalDocument = globalThis.document;
  const originalInput = globalThis.HTMLInputElement;
  const originalKeyboardEvent = globalThis.KeyboardEvent;
  const originalEvent = globalThis.Event;
  const calls = [];

  class FakeInput {
    set value(value) {
      this.currentValue = value;
      calls.push(['value', value]);
    }

    dispatchEvent(event) {
      calls.push([event.type, event.key]);
      return true;
    }
  }

  class FakeEvent {
    constructor(type, options) {
      this.type = type;
      Object.assign(this, options);
    }
  }

  const input = new FakeInput();
  globalThis.HTMLInputElement = FakeInput;
  globalThis.Event = FakeEvent;
  globalThis.KeyboardEvent = FakeEvent;
  globalThis.document = { querySelector: () => input };

  try {
    assert.deepEqual(await fillAndSubmit('BTC'), { ok: true });
    assert.deepEqual(calls, [
      ['value', 'BTC'],
      ['input', undefined],
      ['keydown', 'Enter']
    ]);
  } finally {
    globalThis.document = originalDocument;
    globalThis.HTMLInputElement = originalInput;
    globalThis.KeyboardEvent = originalKeyboardEvent;
    globalThis.Event = originalEvent;
  }
});

test('page action waits before clicking Add Pair', async () => {
  const originalDocument = globalThis.document;
  const originalInput = globalThis.HTMLInputElement;
  const originalKeyboardEvent = globalThis.KeyboardEvent;
  const originalEvent = globalThis.Event;
  const originalXPathResult = globalThis.XPathResult;
  const originalSetTimeout = globalThis.setTimeout;
  const calls = [];

  class FakeInput {
    set value(value) { calls.push(['value', value]); }
    dispatchEvent(event) { calls.push([event.type, event.key]); return true; }
  }
  class FakeEvent {
    constructor(type, options) { this.type = type; Object.assign(this, options); }
  }
  const button = { click: () => calls.push(['click']) };
  globalThis.HTMLInputElement = FakeInput;
  globalThis.Event = FakeEvent;
  globalThis.KeyboardEvent = FakeEvent;
  globalThis.XPathResult = { FIRST_ORDERED_NODE_TYPE: 9 };
  globalThis.setTimeout = (callback, delay) => {
    calls.push(['delay', delay]);
    callback();
    return 1;
  };
  globalThis.document = {
    querySelector: () => new FakeInput(),
    evaluate: () => ({ singleNodeValue: button })
  };

  try {
    assert.deepEqual(await fillAndSubmit('BTC', true), { ok: true });
    assert.deepEqual(calls, [
      ['value', 'BTC'],
      ['input', undefined],
      ['keydown', 'Enter'],
      ['delay', 1000],
      ['click']
    ]);
  } finally {
    globalThis.document = originalDocument;
    globalThis.HTMLInputElement = originalInput;
    globalThis.KeyboardEvent = originalKeyboardEvent;
    globalThis.Event = originalEvent;
    globalThis.XPathResult = originalXPathResult;
    globalThis.setTimeout = originalSetTimeout;
  }
});
