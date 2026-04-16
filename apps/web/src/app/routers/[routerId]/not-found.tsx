import { RouteNotFoundState } from "~/components/route-state";

export default function RouterDetailNotFound() {
  return (
    <RouteNotFoundState
      eyebrow="Роутер"
      title="Роутер не найден"
      description="Этот маршрут подтвердил отсутствие целевого роутера и показывает отдельное операторское состояние отсутствия объекта."
      summary="Сервер не знает роутер по этому идентификатору."
      details="Проверьте ссылку, вернитесь в `Парк` и заново откройте нужное устройство из списка. Если роутер недавно удалили, этот маршрут уже не сможет собрать его рабочую поверхность."
      actions={[
        { href: "/fleet", label: "Вернуться в Парк", tone: "primary" },
        { href: "/drafts", label: "Открыть Черновики" },
      ]}
    />
  );
}
