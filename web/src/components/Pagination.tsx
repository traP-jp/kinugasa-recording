import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "./Button";
import type { Pagination as PaginationData } from "../api/types";

interface PaginationProps {
  value: PaginationData;
  onChange: (page: number) => void;
}

export function Pagination({ value, onChange }: PaginationProps) {
  const pageCount = Math.max(1, Math.ceil(value.total / value.pageSize));
  return (
    <div className="pagination" aria-label="ページ選択">
      <Button
        variant="quiet"
        icon={<ChevronLeft size={16} />}
        disabled={value.page <= 1}
        onClick={() => onChange(value.page - 1)}
      >前へ</Button>
      <span>{value.page} / {pageCount}</span>
      <Button
        variant="quiet"
        icon={<ChevronRight size={16} />}
        disabled={value.page >= pageCount}
        onClick={() => onChange(value.page + 1)}
      >次へ</Button>
    </div>
  );
}
