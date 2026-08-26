import { FileCode2 } from 'lucide-react';
import type { ComponentProps } from 'react';

// A file-type marker is more recognisable than a network glyph for a saved
// capture, and stays legible without depending on an OS-specific file icon.
export function PcapFileIcon({ className, ...props }: ComponentProps<typeof FileCode2>) {
  return (
    <span className={`relative inline-flex shrink-0 ${className ?? ''}`} aria-hidden="true">
      <FileCode2 {...props} />
      <span className="absolute -bottom-0.5 -right-1 rounded-sm bg-sky-600 px-0.5 font-mono text-[5px] font-bold leading-[8px] text-white">PCAP</span>
    </span>
  );
}
