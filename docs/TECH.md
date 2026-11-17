# Vide-studio — выбор технологий (черновик, Go)

Vide-studio — единая оболочка для Vimg (пиксельарт), Vtune (чиптюн), VCode (TrueDSL), а также запуска игры и публикации в ViDe-store. В README такого компонента пока нет — состав уточнить.

## Критерии
1. Pure Go, без cgo (простая кросс-сборка, один бинарник).
2. Пиксельарт: целочисленный масштаб, без сглаживания.
3. Общий стек с рантаймом: превью игры внутри студии = тот же код, что в рантайме.
4. Низкая задержка аудио (Vtune).
5. Один процесс, единый набор виджетов для всех редакторов.

## Кандидаты
| Вариант | Плюсы | Минусы |
|---|---|---|
| **Ebitengine v2.10** | Pure Go на Windows/macOS/Linux (с 2.10), 2D, пиксельарт «из коробки», аудио (oto), совпадает с рантаймом | Не GUI-тулкит: нет виджетов; нужен Go 1.25+ |
| Gio | Pure Go, immediate mode | Мало виджетов, API до 1.0, свой цикл — не делится с рантаймом |
| Fyne | Много виджетов, большое сообщество | Нужен cgo, «нативные» виджеты, плохо подходит для пиксельного холста |
| Cogent Core | Retained-mode, богатый | Нужен cgo (Vulkan/glfw), большое дерево зависимостей |
| cimgui-go (Dear ImGui) + backend Ebitengine | Готовые виджеты для инструментов, докинг | Обёртка над C++ → cgo |

(Оценки cgo/виджетов Gio, Fyne, Cogent Core — из одной сравнительной статьи, написанной автором конкурирующего тулкита; перепроверить перед финальным решением.)

## Решение
- **Окно, рендер, ввод, аудио: Ebitengine v2.10+** (`ebiten/v2`, `ebiten/v2/audio` поверх oto, purego под капотом).
- **UI: свой небольшой immediate-mode слой поверх Ebitengine** (кнопка, панель, список, слайдер, поле ввода, вкладки, докинг), чтобы не тянуть cgo. Резерв — `ebitenui` (retained-виджеты; зрелость не проверена).
- **Холст Vimg**: `ebiten.Image` + `FilterNearest`, данные слоёв — `image.RGBA`/индексированные палитры в памяти.
- **Vtune**: собственный синтез (square/pulse, triangle, noise) в `io.Reader` для `audio.Player`; детерминированный секвенсор.
- **VCode**: собственный текстовый буфер + рендер шрифта (`text/v2`), лексер/парсер TrueDSL из пакета языка.
- **Форматы/проекты**: общие пакеты `formats/` (Vimg, Vtune, TrueDSL), общие с рантаймом; версионирование и тесты на повреждённые файлы.
- **Тесты**: логика отдельно от UI (чистые Go-пакеты), `go test`.

## Риски
- Самый тяжёлый узел — многострочный текстовый редактор (IME, выделение, прокрутка) в самописном UI. Если затянется — рассмотреть ebitenui или вынести редактор кода.
- Linux всё равно требует системные библиотеки окна/графики/аудио в рантайме (без заголовков для сборки).
- Go на машине сейчас не установлен.

## Предлагаемая структура
```
cmd/vide-studio/    # точка входа
internal/ui/        # виджеты, докинг, тема
internal/studio/    # оболочка, проекты, вкладки
internal/vimg/  internal/vtune/  internal/vcode/
pkg/formats/        # общий контракт с рантаймом
```

## Источники
- [Ebitengine 2.10 Release Notes](https://ebitengine.org/en/documents/2.10.html)
- [Ebitengine install](https://ebitengine.org/en/documents/install.html)
- [ebitengine/oto](https://github.com/ebitengine/oto/wiki), [purego](https://www.github.com/ebitengine/purego)
- [Сравнение Go GUI (LogRocket)](https://blog.logrocket.com/best-gui-frameworks-go), [gogpu — DEV](https://dev.to/kolkov/gogpu-790k-lines-of-pure-go-multi-window-gpu-apps-dxil-compiler-and-why-we-dont-need-cgo-3i94)
- [cimgui-go](https://github.com/AllenDang/giu)
