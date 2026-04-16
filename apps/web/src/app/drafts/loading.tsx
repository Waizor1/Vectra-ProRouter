import { RouteLoadingState } from "~/components/route-state";

export default function DraftsLoading() {
  return (
    <RouteLoadingState
      eyebrow="Черновики"
      title="Загружаем экспертный JSON workspace"
      description="Открываем резервный экспертный маршрут с явной загрузкой вместо тихого перехода."
      summary="Подтягиваем список ревизий, выбранный роутер и preview-конфиг."
      details="Сначала сервер отдаёт историю ревизий и рабочую поверхность для выбранного роутера, после чего откроется JSON-редактор и preview diff."
      checkpoints={[
        "читаем workspace по черновикам",
        "подтягиваем историю ревизий",
        "готовим preview для редактора",
      ]}
    />
  );
}
