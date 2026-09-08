import { useEffect, useLayoutEffect, useRef, useState } from 'react';

export function turnAnchors(viewport: HTMLElement): Map<string, HTMLElement> {
  const anchors = new Map<string, HTMLElement>();
  for (const node of viewport.querySelectorAll<HTMLElement>('[data-turn-id]')) {
    const id = node.dataset.turnId!;
    const previous = anchors.get(id);
    if (!previous || (node.hasAttribute('data-turn-question') && !previous.hasAttribute('data-turn-question'))) anchors.set(id, node);
  }
  return anchors;
}

export function useConversationNavigation(viewport: HTMLElement | null, following: boolean): string | undefined {
  const [currentTurnID, setCurrentTurnID] = useState<string>();
  const followingRef = useRef(following);
  const scheduleRef = useRef<(() => void) | null>(null);

  useLayoutEffect(() => {
    followingRef.current = following;
    scheduleRef.current?.();
  });

  useEffect(() => {
    if (!viewport) return;
    let frame = 0;
    const measure = () => {
      frame = 0;
      const anchors = [...turnAnchors(viewport)];
      const reference = viewport.getBoundingClientRect().top + 24;
      let next: string | undefined = anchors[0]?.[0];
      if (followingRef.current) next = anchors.at(-1)?.[0];
      else for (const [id, node] of anchors) {
        if (node.getBoundingClientRect().top <= reference + 1) next = id;
      }
      setCurrentTurnID(previous => previous === next ? previous : next);
    };
    const schedule = () => { if (!frame) frame = requestAnimationFrame(measure); };
    scheduleRef.current = schedule;
    const observer = new ResizeObserver(schedule);
    observer.observe(viewport);
    const content = viewport.querySelector('.conversation-content');
    if (content) observer.observe(content);
    viewport.addEventListener('scroll', schedule, { passive: true });
    schedule();
    return () => {
      scheduleRef.current = null;
      cancelAnimationFrame(frame);
      observer.disconnect();
      viewport.removeEventListener('scroll', schedule);
    };
  }, [viewport]);

  return currentTurnID;
}
