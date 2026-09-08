import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent } from 'react';
import { createPortal } from 'react-dom';
import type { TurnNavigationItem } from './turnNavigation';

interface TurnNavigatorProps {
  items: TurnNavigationItem[];
  currentTurnID?: string;
  onSelect: (turnID: string, focusTarget: boolean) => void;
  navigationHost?: HTMLElement | null;
  hasEarlier?: boolean;
}

export function TurnNavigator({ items, currentTurnID, onSelect, navigationHost, hasEarlier = false }: TurnNavigatorProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const toggleRef = useRef<HTMLButtonElement>(null);
  const transferFocusRef = useRef(false);
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const [compact, setCompact] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [preview, setPreview] = useState<{ turnID: string; top: number } | null>(null);
  const [focusedID, setFocusedID] = useState<string>();
  const menuID = useId();
  const previewID = useId();
  const hasItems = items.length > 0;
  const currentIndex = Math.max(0, items.findIndex(item => item.turnID === currentTurnID));
  const focusedIndex = items.findIndex(item => item.turnID === focusedID);
  const tabIndex = focusedIndex < 0 ? currentIndex : focusedIndex;
  const selectedPreview = items.find(item => item.turnID === preview?.turnID);
  const range = `已加载 ${items.length} 轮${hasEarlier ? ' · 更早记录未加载' : ''}`;
  const label = (index: number) => `${hasEarlier ? '已加载' : ''}第 ${index + 1} 轮：${items[index].question}`;

  const reveal = (button: HTMLButtonElement) => {
    const list = listRef.current;
    if (!list) return;
    const row = button.getBoundingClientRect();
    const bounds = list.getBoundingClientRect();
    if (row.top < bounds.top) list.scrollTop += row.top - bounds.top;
    else if (row.bottom > bounds.bottom) list.scrollTop += row.bottom - bounds.bottom;
  };

  const showPreview = (turnID: string) => {
    const button = buttons.current.get(turnID);
    const root = rootRef.current;
    if (!button || !root || compact) return;
    const row = button.getBoundingClientRect();
    const bounds = root.getBoundingClientRect();
    setPreview({ turnID, top: Math.max(0, Math.min(row.top - bounds.top - 50, bounds.height - 170)) });
  };

  const focusItem = (index: number) => {
    const item = items[index];
    const button = item && buttons.current.get(item.turnID);
    if (!button) return;
    setFocusedID(item.turnID);
    button.focus({ preventScroll: true });
    reveal(button);
    showPreview(item.turnID);
  };

  const close = (restoreFocus = false) => {
    setPreview(null);
    setMenuOpen(false);
    if (restoreFocus && compact) toggleRef.current?.focus({ preventScroll: true });
  };

  useEffect(() => {
    const root = rootRef.current;
    const container = root?.parentElement;
    if (!container) return;
    let previous: boolean | undefined;
    const observer = new ResizeObserver(([entry]) => {
      const next = Boolean(navigationHost) && entry.contentRect.width <= 540;
      if (next === previous) return;
      previous = next;
      const hadFocus = root.contains(document.activeElement) || toggleRef.current === document.activeElement;
      transferFocusRef.current = hadFocus;
      setCompact(next);
      setPreview(null);
      setMenuOpen(false);
    });
    observer.observe(container);
    return () => observer.disconnect();
  }, [navigationHost, hasItems]);

  useLayoutEffect(() => {
    if (!transferFocusRef.current) return;
    transferFocusRef.current = false;
    const target = compact ? toggleRef.current : rootRef.current?.querySelector<HTMLButtonElement>('button[tabindex="0"]');
    target?.focus({ preventScroll: true });
  }, [compact]);

  useEffect(() => {
    if (!menuOpen) return;
    const outside = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && !rootRef.current?.contains(target) && !toggleRef.current?.contains(target)) setMenuOpen(false);
    };
    document.addEventListener('pointerdown', outside);
    return () => document.removeEventListener('pointerdown', outside);
  }, [menuOpen]);

  useLayoutEffect(() => {
    const button = buttons.current.get(currentTurnID ?? '');
    if (button && !focusedID) reveal(button);
  }, [currentTurnID, focusedID, compact]);

  useLayoutEffect(() => {
    if (!menuOpen) return;
    const button = buttons.current.get(currentTurnID ?? '') ?? buttons.current.values().next().value;
    button?.focus({ preventScroll: true });
    if (button) reveal(button);
  }, [menuOpen, currentTurnID]);

  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      close(true);
      return;
    }
    const index = items.findIndex(item => buttons.current.get(item.turnID) === event.target);
    if (index < 0) return;
    const target = event.key === 'ArrowDown' ? Math.min(items.length - 1, index + 1)
      : event.key === 'ArrowUp' ? Math.max(0, index - 1)
      : event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : -1;
    if (target >= 0) { event.preventDefault(); focusItem(target); }
  };

  if (items.length === 0) return null;
  const toggle = <button ref={toggleRef} type="button" className="turn-navigation-toggle" aria-label="轮次导航"
    aria-expanded={menuOpen} aria-controls={menuID} onClick={() => setMenuOpen(open => !open)}>轮次导航</button>;
  return <div ref={rootRef} className={`turn-navigation${compact ? ' compact' : ''}`} onKeyDown={onKeyDown}
    onMouseLeave={() => setPreview(null)} onBlur={event => {
      if (!event.currentTarget.contains(event.relatedTarget) && event.relatedTarget !== toggleRef.current) close();
    }}>
    {compact && navigationHost && createPortal(toggle, navigationHost)}
    {(!compact || menuOpen) && <div className={compact ? 'turn-menu' : 'turn-ruler'} id={compact ? menuID : undefined}
      role={compact ? 'dialog' : 'navigation'} aria-label="会话轮次">
      <div className="turn-range" title={range}>{compact ? range : <><span className="turn-range-count">{currentIndex + 1}/{items.length}</span>{hasEarlier && <span>已加载</span>}</>}</div>
      <div className="turn-list" ref={listRef} onScroll={() => { if (preview) showPreview(preview.turnID); }}>
        {items.map((item, index) => <button type="button" key={item.turnID}
          ref={button => { if (button) buttons.current.set(item.turnID, button); else buttons.current.delete(item.turnID); }}
          className={compact ? 'turn-menu-item' : 'turn-tick'} aria-label={label(index)}
          aria-current={item.turnID === currentTurnID ? 'true' : undefined}
          aria-describedby={preview?.turnID === item.turnID ? previewID : undefined}
          tabIndex={index === tabIndex ? 0 : -1}
          onMouseEnter={() => showPreview(item.turnID)} onFocus={() => { setFocusedID(item.turnID); showPreview(item.turnID); }}
          onClick={() => { close(); onSelect(item.turnID, true); }}>
          {compact ? <><span className="turn-number">{index + 1}</span><span><strong>{item.question}</strong><small>{item.answer}</small></span></> : <span aria-hidden="true" />}
        </button>)}
      </div>
    </div>}
    {!compact && selectedPreview && <div role="tooltip" id={previewID} className="turn-preview" style={{ top: preview?.top }}>
      <div className="turn-preview-card"><strong>{selectedPreview.question}</strong><p>{selectedPreview.answer}</p><small>{range}</small></div>
    </div>}
  </div>;
}
