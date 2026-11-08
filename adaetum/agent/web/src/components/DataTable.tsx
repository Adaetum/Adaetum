import {
  ColumnDef,
  SortingState,
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { Box, Flex, IconButton, Table, Text, TextField } from "@radix-ui/themes";
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight, Search } from "lucide-react";
import { ReactNode, useState } from "react";

interface DataTableProps<T> {
  data: T[];
  // Columns use `any` for the value type because accessors vary per column.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  columns: ColumnDef<T, any>[];
  getRowId: (row: T) => string;
  // Noun for the footer count, e.g. "pods".
  noun: string;
  initialSorting?: SortingState;
  // Extra controls shown beside the search box, such as filters.
  toolbar?: ReactNode;
  empty?: ReactNode;
  pageSize?: number;
}

// DataTable is the one table every view uses: sortable, searchable, paginated.
// Filtering that depends on page state (namespace, health) is applied by the
// caller before data reaches the table, so the footer count stays honest.
export function DataTable<T>({
  data,
  columns,
  getRowId,
  noun,
  initialSorting = [],
  toolbar,
  empty = "Nothing to show.",
  pageSize = 25,
}: DataTableProps<T>) {
  const [sorting, setSorting] = useState<SortingState>(initialSorting);
  const [globalFilter, setGlobalFilter] = useState("");

  const table = useReactTable({
    data,
    columns,
    getRowId,
    state: { sorting, globalFilter },
    onSortingChange: setSorting,
    onGlobalFilterChange: setGlobalFilter,
    globalFilterFn: "includesString",
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageSize } },
  });

  const rows = table.getRowModel().rows;
  const total = table.getFilteredRowModel().rows.length;
  const { pageIndex, pageSize: size } = table.getState().pagination;
  const first = total === 0 ? 0 : pageIndex * size + 1;
  const last = Math.min(total, (pageIndex + 1) * size);

  return (
    <Flex direction="column" gap="3">
      <Flex gap="3" align="center" wrap="wrap">
        <Box style={{ flex: "1 1 220px", maxWidth: 360 }}>
          <TextField.Root
            placeholder={`Search ${noun}`}
            aria-label={`Search ${noun}`}
            value={globalFilter}
            onChange={(event) => setGlobalFilter(event.target.value)}
          >
            <TextField.Slot>
              <Search size={16} aria-hidden />
            </TextField.Slot>
          </TextField.Root>
        </Box>
        {toolbar}
      </Flex>

      <Box className="table-scroll">
        <Table.Root variant="surface" size="2">
          <Table.Header>
            {table.getHeaderGroups().map((group) => (
              <Table.Row key={group.id}>
                {group.headers.map((header) => {
                  const sorted = header.column.getIsSorted();
                  const sortable = header.column.getCanSort();
                  return (
                    <Table.ColumnHeaderCell
                      key={header.id}
                      aria-sort={sorted === "asc" ? "ascending" : sorted === "desc" ? "descending" : undefined}
                    >
                      {sortable ? (
                        <button type="button" className="sort-button" onClick={header.column.getToggleSortingHandler()}>
                          {flexRender(header.column.columnDef.header, header.getContext())}
                          {sorted === "asc" ? (
                            <ArrowUp size={14} aria-hidden />
                          ) : sorted === "desc" ? (
                            <ArrowDown size={14} aria-hidden />
                          ) : (
                            <ArrowUpDown size={14} aria-hidden className="sort-idle" />
                          )}
                        </button>
                      ) : (
                        flexRender(header.column.columnDef.header, header.getContext())
                      )}
                    </Table.ColumnHeaderCell>
                  );
                })}
              </Table.Row>
            ))}
          </Table.Header>
          <Table.Body>
            {rows.length === 0 ? (
              <Table.Row>
                <Table.Cell colSpan={columns.length}>
                  <Box py="5">
                    <Text color="gray" size="2" as="div" align="center">
                      {globalFilter ? `No ${noun} match “${globalFilter}”.` : empty}
                    </Text>
                  </Box>
                </Table.Cell>
              </Table.Row>
            ) : (
              rows.map((row) => (
                <Table.Row key={row.id} align="center">
                  {row.getVisibleCells().map((cell) => (
                    <Table.Cell key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</Table.Cell>
                  ))}
                </Table.Row>
              ))
            )}
          </Table.Body>
        </Table.Root>
      </Box>

      <Flex justify="between" align="center">
        <Text size="2" color="gray">
          {total === 0 ? `0 ${noun}` : `${first}–${last} of ${total} ${noun}`}
        </Text>
        {table.getPageCount() > 1 && (
          <Flex gap="2">
            <IconButton variant="soft" color="gray" aria-label="Previous page" disabled={!table.getCanPreviousPage()} onClick={() => table.previousPage()}>
              <ChevronLeft size={16} />
            </IconButton>
            <IconButton variant="soft" color="gray" aria-label="Next page" disabled={!table.getCanNextPage()} onClick={() => table.nextPage()}>
              <ChevronRight size={16} />
            </IconButton>
          </Flex>
        )}
      </Flex>
    </Flex>
  );
}
