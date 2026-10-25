package bpm

// Переменные, в которые движок кладёт ошибку задачи перед переходом по граничному событию ошибки
const (
	ErrorCodeVariable    = "errorCode"
	ErrorMessageVariable = "errorMessage"
)

// Error - бизнес-ошибка BPMN. Метод task или обработчик service task возвращает её, чтобы процесс
// сразу, без повторов, пошёл по граничному событию ошибки с этим кодом (или по событию без кода).
// Если события нет, задача service task падает без повторов, а шаг с методом task откатывается
type Error struct {
	Code    string
	Message string
}

func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func (e *Error) Error() string {
	if e.Message == "" {
		return "bpmn error " + e.Code
	}
	return "bpmn error " + e.Code + ": " + e.Message
}
