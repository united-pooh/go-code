import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { vi } from 'vitest';
import { TurnNavigator } from './TurnNavigator';

const items = [
  { turnID: 'a', question: '问题一', answer: '<b>答案一</b>' },
  { turnID: 'b', question: '问题二', answer: '答案二' },
  { turnID: 'c', question: '问题三', answer: '答案三' },
];

it('previews plain text on hover/focus and selects once', async () => {
  const onSelect = vi.fn();
  const user = userEvent.setup();
  render(<div><TurnNavigator items={items} currentTurnID="b" onSelect={onSelect} hasEarlier /></div>);
  const first = screen.getByRole('button', { name: '已加载第 1 轮：问题一' });
  expect(screen.getByRole('button', { name: '已加载第 2 轮：问题二' })).toHaveAttribute('aria-current', 'true');
  await user.hover(first);
  expect(screen.getByRole('tooltip')).toHaveTextContent('<b>答案一</b>');
  expect(screen.getByRole('tooltip').querySelector('b')).toBeNull();
  fireEvent.keyDown(first, { key: 'Escape' });
  expect(screen.queryByRole('tooltip')).toBeNull();
  await user.click(first);
  expect(onSelect).toHaveBeenCalledExactlyOnceWith('a', true);
  expect(screen.queryByRole('tooltip')).toBeNull();
});

it('uses one tab stop and arrows/Home/End to preview without selecting', async () => {
  const onSelect = vi.fn();
  const user = userEvent.setup();
  render(<div><TurnNavigator items={items} currentTurnID="a" onSelect={onSelect} /></div>);
  await user.tab();
  expect(screen.getByRole('button', { name: '第 1 轮：问题一' })).toHaveFocus();
  await user.keyboard('{ArrowDown}');
  expect(screen.getByRole('button', { name: '第 2 轮：问题二' })).toHaveFocus();
  expect(screen.getByRole('tooltip')).toHaveTextContent('答案二');
  await user.keyboard('{End}');
  expect(screen.getByRole('button', { name: '第 3 轮：问题三' })).toHaveFocus();
  await user.keyboard('{Home}{Enter}');
  expect(onSelect).toHaveBeenCalledExactlyOnceWith('a', true);
});

it('opens a narrow menu in the topbar slot and closes with Escape/outside/selection', async () => {
  let resize: ResizeObserverCallback = () => undefined;
  vi.stubGlobal('ResizeObserver', class {
    constructor(callback: ResizeObserverCallback) { resize = callback; }
    observe() {} disconnect() {}
  });
  const host = document.createElement('div');
  document.body.append(host);
  const onSelect = vi.fn();
  const user = userEvent.setup();
  try {
    render(<div><TurnNavigator items={items} currentTurnID="a" onSelect={onSelect} navigationHost={host} hasEarlier /></div>);
    act(() => resize([{ contentRect: { width: 390 } } as ResizeObserverEntry], {} as ResizeObserver));
    const toggle = screen.getByRole('button', { name: '轮次导航' });
    expect(host).toContainElement(toggle);
    await user.click(toggle);
    expect(screen.getByText('已加载 3 轮 · 更早记录未加载')).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(toggle).toHaveFocus();
    expect(screen.queryByRole('dialog')).toBeNull();
    await user.click(toggle);
    await user.click(document.body);
    expect(screen.queryByRole('dialog')).toBeNull();
    await user.click(toggle);
    await user.click(screen.getByRole('button', { name: '已加载第 2 轮：问题二' }));
    expect(onSelect).toHaveBeenCalledExactlyOnceWith('b', true);
    expect(screen.queryByRole('dialog')).toBeNull();
  } finally {
    host.remove();
    vi.unstubAllGlobals();
  }
});

it('starts observing after an empty session receives its first visible turn and cleans up', () => {
  const observe = vi.fn();
  const disconnect = vi.fn();
  vi.stubGlobal('ResizeObserver', class { observe = observe; disconnect = disconnect; });
  try {
    const view = render(<div><TurnNavigator items={[]} onSelect={() => undefined} /></div>);
    expect(observe).not.toHaveBeenCalled();
    view.rerender(<div><TurnNavigator items={items} onSelect={() => undefined} /></div>);
    expect(observe).toHaveBeenCalledOnce();
    view.unmount();
    expect(disconnect).toHaveBeenCalledOnce();
  } finally { vi.unstubAllGlobals(); }
});
