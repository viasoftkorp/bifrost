import InventoryView from "@enterprise/components/edge-control/inventoryView";

export default function EdgeInventoryPage() {
	return (
		<div className="no-padding-parent mx-auto flex h-[calc(100dvh-1rem)] w-full max-w-7xl p-4">
			<InventoryView />
		</div>
	);
}
