package bslhelp

import "strings"

type Entry struct {
	Name        string   `json:"name"`
	Signature   string   `json:"signature"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases,omitempty"`
}

type Help struct {
	entries []Entry
}

func Default() *Help {
	return &Help{entries: []Entry{
		{Name: "Если", Signature: "Если Условие Тогда ... ИначеЕсли ... Иначе ... КонецЕсли;", Description: "Условный оператор.", Aliases: []string{"If"}},
		{Name: "Для каждого", Signature: "Для Каждого Элемент Из Коллекция Цикл ... КонецЦикла;", Description: "Перебор элементов коллекции.", Aliases: []string{"For Each"}},
		{Name: "Для", Signature: "Для Счетчик = Начало По Конец Цикл ... КонецЦикла;", Description: "Цикл со счетчиком.", Aliases: []string{"For"}},
		{Name: "Пока", Signature: "Пока Условие Цикл ... КонецЦикла;", Description: "Цикл с условием.", Aliases: []string{"While"}},
		{Name: "Процедура", Signature: "Процедура Имя(Параметры) Экспорт ... КонецПроцедуры", Description: "Объявляет процедуру.", Aliases: []string{"Procedure"}},
		{Name: "Функция", Signature: "Функция Имя(Параметры) Экспорт ... Возврат Значение; КонецФункции", Description: "Объявляет функцию.", Aliases: []string{"Function"}},
		{Name: "Перем", Signature: "Перем Имя Экспорт;", Description: "Объявляет переменную модуля.", Aliases: []string{"Var"}},
		{Name: "Новый", Signature: "Значение = Новый Тип(Параметры);", Description: "Создает значение указанного типа.", Aliases: []string{"New"}},
		{Name: "Возврат", Signature: "Возврат Значение;", Description: "Завершает функцию и возвращает значение.", Aliases: []string{"Return"}},
		{Name: "Продолжить", Signature: "Продолжить;", Description: "Переходит к следующей итерации цикла.", Aliases: []string{"Continue"}},
		{Name: "Прервать", Signature: "Прервать;", Description: "Завершает ближайший цикл.", Aliases: []string{"Break"}},
		{Name: "ВызватьИсключение", Signature: "ВызватьИсключение ТекстОшибки;", Description: "Прерывает выполнение с исключением."},
		{Name: "НачатьТранзакцию", Signature: "НачатьТранзакцию(); ... ЗафиксироватьТранзакцию();", Description: "Начинает транзакцию; при исключении используется ОтменитьТранзакцию()."},
		{Name: "СтрНайти", Signature: "СтрНайти(Строка, Подстрока)", Description: "Возвращает позицию первого вхождения подстроки или 0.", Aliases: []string{"StrFind"}},
		{Name: "СтрЗаменить", Signature: "СтрЗаменить(Строка, ПодстрокаПоиска, ПодстрокаЗамены)", Description: "Заменяет все вхождения одной подстроки другой.", Aliases: []string{"StrReplace"}},
		{Name: "ЗначениеЗаполнено", Signature: "ЗначениеЗаполнено(Значение)", Description: "Проверяет, заполнено ли значение прикладного типа."},
		{Name: "Новый Запрос", Signature: "Запрос = Новый Запрос(ТекстЗапроса)", Description: "Создает объект запроса к данным 1С."},
		{Name: "УстановитьПараметр", Signature: "Запрос.УстановитьПараметр(Имя, Значение)", Description: "Передает параметр в запрос."},
		{Name: "Выполнить", Signature: "Запрос.Выполнить()", Description: "Выполняет запрос и возвращает результат запроса."},
		{Name: "Выбрать", Signature: "РезультатЗапроса.Выбрать()", Description: "Возвращает выборку по результату запроса."},
		{Name: "ТекущаяДата", Signature: "ТекущаяДата()", Description: "Возвращает текущую дату и время сеанса."},
		{Name: "Сообщить", Signature: "Сообщить(ТекстСообщения)", Description: "Выводит сообщение пользователю."},
		{Name: "Попытка", Signature: "Попытка ... Исключение ... КонецПопытки", Description: "Обрабатывает исключения BSL."},
	}}
}

func (h *Help) Search(query string, limit int) []Entry {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		if len(h.entries) > limit {
			return h.entries[:limit]
		}
		return h.entries
	}
	out := make([]Entry, 0)
	for _, entry := range h.entries {
		haystack := strings.ToLower(entry.Name + " " + entry.Signature + " " + entry.Description + " " + strings.Join(entry.Aliases, " "))
		if strings.Contains(haystack, query) {
			out = append(out, entry)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
